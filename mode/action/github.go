package action

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// apiVersion pins the GitHub REST API version on every request.
const apiVersion = "2022-11-28"

// Pull is the subset of a pull request the review needs.
type Pull struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Base   struct {
		SHA string `json:"sha"`
	} `json:"base"`
	Head struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

// File is one changed file in a pull request. Patch is absent for binary or
// very large files.
type File struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Patch            string `json:"patch"`
}

// ReviewComment is one inline comment in a posted review.
type ReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

// ReviewRequest is the body of POST .../reviews.
type ReviewRequest struct {
	Body     string          `json:"body"`
	Event    string          `json:"event"`
	Comments []ReviewComment `json:"comments"`
}

// Review is a posted review.
type Review struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

// APIError is a non-success HTTP response. It never carries the token.
type APIError struct {
	Status int
	Method string
	Path   string
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github: %s %s: status %d: %s", e.Method, e.Path, e.Status, e.Body)
}

// GitHub is a small GitHub REST client.
type GitHub struct {
	BaseURL string
	Token   string
	Client  *http.Client
	// Sleep waits between retries; tests replace it to avoid real time.
	Sleep func(context.Context, time.Duration) error
}

// NewGitHub returns a client with sensible defaults.
func NewGitHub(baseURL, token string) *GitHub {
	return &GitHub{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, Client: http.DefaultClient, Sleep: sleep}
}

// String redacts the token so a stray %v of the client never prints it.
func (g *GitHub) String() string { return "action.GitHub{" + g.BaseURL + "}" }

// sleep is the real retry wait.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Pull fetches one pull request.
func (g *GitHub) Pull(ctx context.Context, repo string, number int) (*Pull, error) {
	var pull Pull
	if _, err := g.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", repo, number), nil, &pull); err != nil {
		return nil, err
	}
	return &pull, nil
}

// Files lists the pull request's files across pages, capped at 300. The bool
// reports that the pull request has more files than the cap.
func (g *GitHub) Files(ctx context.Context, repo string, number int) ([]File, bool, error) {
	var all []File
	for page := 1; page <= 100; page++ {
		path := fmt.Sprintf("/repos/%s/pulls/%d/files?per_page=100&page=%d", repo, number, page)
		var files []File
		if _, err := g.do(ctx, http.MethodGet, path, nil, &files); err != nil {
			return nil, false, err
		}
		for _, f := range files {
			if len(all) == 300 {
				return all, true, nil
			}
			all = append(all, f)
		}
		if len(files) < 100 {
			break
		}
	}
	return all, false, nil
}

// Reviews lists the reviews already on a pull request.
func (g *GitHub) Reviews(ctx context.Context, repo string, number int) ([]Review, error) {
	var reviews []Review
	path := fmt.Sprintf("/repos/%s/pulls/%d/reviews?per_page=100", repo, number)
	if _, err := g.do(ctx, http.MethodGet, path, nil, &reviews); err != nil {
		return nil, err
	}
	return reviews, nil
}

// PostReview posts one review and returns it.
func (g *GitHub) PostReview(ctx context.Context, repo string, number int, req ReviewRequest) (*Review, error) {
	var review Review
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number)
	if _, err := g.do(ctx, http.MethodPost, path, body, &review); err != nil {
		return nil, err
	}
	return &review, nil
}

// do sends one request, retrying 403/429 and 5xx as documented.
func (g *GitHub) do(ctx context.Context, method, path string, body []byte, out any) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, g.BaseURL+path, reader)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+g.Token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", apiVersion)
		req.Header.Set("User-Agent", "nova-action")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := g.Client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out != nil {
				err = json.NewDecoder(resp.Body).Decode(out)
			}
			resp.Body.Close()
			return resp, err
		}
		detail := readErrorBody(resp)
		retryable := resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode >= 500
		if retryable && attempt < 3 {
			delay := retryDelay(resp, attempt)
			resp.Body.Close()
			if err := g.Sleep(ctx, delay); err != nil {
				return nil, err
			}
			continue
		}
		resp.Body.Close()
		return resp, &APIError{Status: resp.StatusCode, Method: method, Path: path, Body: detail}
	}
}

// retryDelay reads Retry-After, capped at 60s, or backs off exponentially.
func retryDelay(resp *http.Response, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && seconds > 0 {
		if d := time.Duration(seconds) * time.Second; d < 60*time.Second {
			return d
		}
		return 60 * time.Second
	}
	return time.Duration(1<<attempt) * time.Second
}

// readErrorBody returns a short, safe copy of an error body.
func readErrorBody(resp *http.Response) string {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return strings.TrimSpace(string(data))
}
