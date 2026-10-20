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

// maxResponseBytes caps the part of a response body that is read and decoded. It
// is a variable so a test can lower it.
var maxResponseBytes int64 = 64 << 20

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
	Filename string `json:"filename"`
	Patch    string `json:"patch"`
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

// PostedReview is a review returned by the API.
type PostedReview struct {
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

// requestTimeout bounds one GitHub request, so a stalled connection cannot hold
// a workflow job until its own limit.
const requestTimeout = 60 * time.Second

// NewGitHub returns a client with sensible defaults.
func NewGitHub(baseURL, token string) *GitHub {
	return &GitHub{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		Client:  &http.Client{Timeout: requestTimeout},
		Sleep:   sleep,
	}
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
	if err := g.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", repo, number), nil, &pull); err != nil {
		return nil, err
	}
	return &pull, nil
}

// Files lists the pull request's files across pages, capped at 300. The bool
// reports that the pull request has more files than the cap.
func (g *GitHub) Files(ctx context.Context, repo string, number int) ([]File, bool, error) {
	var all []File
	for page := 1; page <= 100; page++ {
		var files []File
		path := fmt.Sprintf("/repos/%s/pulls/%d/files?per_page=100&page=%d", repo, number, page)
		if err := g.do(ctx, http.MethodGet, path, nil, &files); err != nil {
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

// list decodes a JSON array of T from a GET. It is a function, not a method,
// because a method cannot have a type parameter.
func list[T any](ctx context.Context, g *GitHub, path string) ([]T, error) {
	var out []T
	err := g.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// Reviews lists the reviews already on a pull request.
func (g *GitHub) Reviews(ctx context.Context, repo string, number int) ([]PostedReview, error) {
	return list[PostedReview](ctx, g, fmt.Sprintf("/repos/%s/pulls/%d/reviews?per_page=100", repo, number))
}

// ReviewComments lists the inline comments already on a pull request.
func (g *GitHub) ReviewComments(ctx context.Context, repo string, number int) ([]ReviewComment, error) {
	return list[ReviewComment](ctx, g, fmt.Sprintf("/repos/%s/pulls/%d/comments?per_page=100", repo, number))
}

// PostReview posts one review and returns it.
func (g *GitHub) PostReview(ctx context.Context, repo string, number int, req ReviewRequest) (*PostedReview, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var review PostedReview
	path := fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number)
	if err := g.do(ctx, http.MethodPost, path, body, &review); err != nil {
		return nil, err
	}
	return &review, nil
}

// maxRetries is how many times a request is repeated after a retryable answer.
const maxRetries = 3

// do sends one request and decodes a successful answer into out. It retries a
// rate-limited request, and a read that met a server error, with a wait between.
func (g *GitHub) do(ctx context.Context, method, path string, body []byte, out any) error {
	for attempt := 0; ; attempt++ {
		resp, err := g.send(ctx, method, path, body)
		if err != nil {
			return err
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			defer resp.Body.Close()
			if out == nil {
				return nil
			}
			return json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(out)
		}
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if attempt < maxRetries && retryable(method, resp) {
			if err := g.Sleep(ctx, retryDelay(resp, attempt)); err != nil {
				return err
			}
			continue
		}
		return &APIError{Status: resp.StatusCode, Method: method, Path: path, Body: strings.TrimSpace(string(detail))}
	}
}

// send builds and sends one authenticated request.
func (g *GitHub) send(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
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
	return g.Client.Do(req)
}

// retryable reports whether asking again can help. A rate-limited answer was
// refused before anything happened, so any method may repeat it. A server error
// may have been acted on, so only a read is repeated: asking again on a POST
// could post the same review twice. Other 403 answers are permissions, which
// waiting does not change.
func retryable(method string, resp *http.Response) bool {
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return true
	case http.StatusForbidden:
		return resp.Header.Get("Retry-After") != "" || resp.Header.Get("X-RateLimit-Remaining") == "0"
	}
	return resp.StatusCode >= 500 && method == http.MethodGet
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
