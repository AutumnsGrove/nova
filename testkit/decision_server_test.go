package testkit

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// postJSON sends a JSON body and returns the status and response body.
func postJSON(t *testing.T, url, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(out)
}

func TestDecisionServer_AnswersBothSurfaces(t *testing.T) {
	s := NewDecisionServer(t,
		DecisionServerTurn{Body: `{"answers":{"q":{"choice":"a"}},"model":"build-1"}`},
		DecisionServerTurn{Status: http.StatusBadRequest, Body: `{"error":{"message":"bad question"}}`},
	)

	status, body := postJSON(t, s.URL+"/api/alpha/decisions", `{"model":"m","state":"s","questions":{}}`)
	if status != http.StatusOK || !strings.Contains(body, "build-1") {
		t.Fatalf("decisions response = %d %s", status, body)
	}
	status, body = postJSON(t, s.URL+"/api/v1/systemone", `{"model":"m","state":"s","questions":{}}`)
	if status != http.StatusBadRequest || !strings.Contains(body, "bad question") {
		t.Fatalf("systemone response = %d %s", status, body)
	}

	reqs := s.Requests()
	if len(reqs) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(reqs))
	}
	if reqs[0].Path != "/api/alpha/decisions" || reqs[1].Path != "/api/v1/systemone" {
		t.Fatalf("paths = %q, %q", reqs[0].Path, reqs[1].Path)
	}
	if got := string(reqs[0].Body); got != `{"model":"m","state":"s","questions":{}}` {
		t.Fatalf("recorded body = %q", got)
	}
	if got := reqs[0].Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestDecisionServer_ExhaustedAndUnknownPath(t *testing.T) {
	s := NewDecisionServer(t)
	status, body := postJSON(t, s.URL+"/api/alpha/decisions", `{}`)
	if status != http.StatusInternalServerError || !strings.Contains(body, "exhausted") {
		t.Fatalf("exhausted response = %d %s", status, body)
	}
	status, _ = postJSON(t, s.URL+"/not-a-decision-path", `{}`)
	if status != http.StatusNotFound {
		t.Fatalf("unknown path status = %d, want 404", status)
	}
}
