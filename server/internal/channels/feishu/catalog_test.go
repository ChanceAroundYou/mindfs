package feishu

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/nodes"
)

type fakeNodeLister struct{ items []nodes.NodeConnection }

func (l fakeNodeLister) List() []nodes.NodeConnection { return l.items }

func TestProjectCatalogMergesNodesAndCaches(t *testing.T) {
	local := func() []ProjectRef {
		return []ProjectRef{{NodeID: "home", RootID: "proj", Name: "proj"}}
	}
	lister := fakeNodeLister{items: []nodes.NodeConnection{
		{ID: "fn", Name: "fn", URL: "http://fn:7331/mindfs", Remote: true},
		{ID: "pc", Name: "pc", URL: "http://pc:7331/mindfs-win/", Remote: true},
	}}
	urls := make([]string, 0, 4)
	fetch := func(_ context.Context, url string) ([]byte, error) {
		urls = append(urls, url)
		switch url {
		case "http://fn:7331/mindfs/api/dirs":
			return []byte(`[{"id":"docs","display_name":"Docs","root_path":"/docs"}]`), nil
		case "http://pc:7331/mindfs-win/api/dirs":
			return []byte(`[{"id":"mindfs","display_name":"MindFS","root_path":"/mindfs"}]`), nil
		default:
			return nil, errors.New("unexpected url " + url)
		}
	}
	clock := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	catalog := NewProjectCatalog(lister, local, 60*time.Second, func() time.Time { return clock }, fetch)

	projects, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 3 {
		t.Fatalf("want 3 projects, got %+v", projects)
	}
	want := []ProjectRef{
		{NodeID: "home", RootID: "proj", Name: "proj"},
		{NodeID: "fn", RootID: "docs", Name: "Docs · fn"},
		{NodeID: "pc", RootID: "mindfs", Name: "MindFS · pc"},
	}
	for i := range want {
		if projects[i] != want[i] {
			t.Fatalf("project %d: want %+v got %+v", i, want[i], projects[i])
		}
	}
	if len(urls) != 2 {
		t.Fatalf("first List must fetch both remote nodes, got %v", urls)
	}

	clock = clock.Add(59 * time.Second)
	if _, err := catalog.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 {
		t.Fatalf("within TTL must not refetch, got %v", urls)
	}

	clock = clock.Add(2 * time.Second)
	if _, err := catalog.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(urls) != 4 {
		t.Fatalf("after TTL must refetch both remote nodes, got %v", urls)
	}
}

func TestProjectCatalogIgnoresFailedNode(t *testing.T) {
	lister := fakeNodeLister{items: []nodes.NodeConnection{
		{ID: "fn", Name: "fn", URL: "http://fn/api", Remote: true},
		{ID: "bad", Name: "bad", URL: "http://bad/api", Remote: true},
	}}
	fetch := func(_ context.Context, url string) ([]byte, error) {
		if strings.HasPrefix(url, "http://bad/") {
			return []byte("internal error"), errors.New("500 from node")
		}
		return []byte(`[{"id":"docs","display_name":"docs"}]`), nil
	}
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	catalog := NewProjectCatalog(lister, nil, time.Minute, time.Now, fetch)
	projects, err := catalog.List(context.Background())
	if err != nil {
		t.Fatalf("one failed node must not fail the whole catalog: %v", err)
	}
	if len(projects) != 1 || projects[0] != (ProjectRef{NodeID: "fn", RootID: "docs", Name: "docs · fn"}) {
		t.Fatalf("want surviving node only, got %+v", projects)
	}
	if got := strings.Count(logs.String(), "bad"); got != 1 {
		t.Fatalf("want exactly one error line for failed node, logs=%q", logs.String())
	}
}

func TestProjectCatalogFormatsLabel(t *testing.T) {
	lister := fakeNodeLister{items: []nodes.NodeConnection{
		{ID: "fn", Name: "fn", URL: "http://fn/api", Remote: true},
		{ID: "other", Name: "other", URL: "http://other/api", Remote: true},
	}}
	fetch := func(_ context.Context, url string) ([]byte, error) {
		if strings.HasPrefix(url, "http://other/") {
			return []byte(`[{"id":"","display_name":"skip"},{"id":"x","display_name":""}]`), nil
		}
		return []byte(`[{"id":"docs","display_name":"docs"}]`), nil
	}
	catalog := NewProjectCatalog(lister, nil, time.Minute, time.Now, fetch)
	projects, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Name != "docs · fn" {
		t.Fatalf("empty id/name must be skipped and label must be name · node, got %+v", projects)
	}
}

func TestProjectCatalogAllNodesFailed(t *testing.T) {
	lister := fakeNodeLister{items: []nodes.NodeConnection{
		{ID: "fn", Name: "fn", URL: "http://fn/api", Remote: true},
	}}
	fetch := func(_ context.Context, _ string) ([]byte, error) {
		return nil, errors.New("offline")
	}
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	catalog := NewProjectCatalog(lister, nil, time.Minute, time.Now, fetch)
	if _, err := catalog.List(context.Background()); err == nil {
		t.Fatal("all remote nodes failed with no local projects must fail closed")
	}
}

func TestProjectCatalogFetchHasFiveSecondDeadline(t *testing.T) {
	lister := fakeNodeLister{items: []nodes.NodeConnection{
		{ID: "fn", Name: "fn", URL: "http://fn/api", Remote: true},
	}}
	fetch := func(ctx context.Context, _ string) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			return nil, errors.New("fetch context has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > 5*time.Second {
			return nil, errors.New("fetch deadline is not 5s")
		}
		return []byte(`[]`), nil
	}
	catalog := NewProjectCatalog(lister, nil, time.Minute, time.Now, fetch)
	if _, err := catalog.List(context.Background()); err != nil {
		t.Fatal(err)
	}
}
