package providers

import "testing"

func TestGetRepoName(t *testing.T) {
	want := "github.com/sourcegraph/sourcegraph"
	urls := []string{
		"git@github.com:sourcegraph/sourcegraph.git",
		"https://github.com/sourcegraph/sourcegraph.git",
		"git://github.com/sourcegraph/sourcegraph.git",
		"http://github.com/sourcegraph/sourcegraph.git",
		"https://username:password@github.com/sourcegraph/sourcegraph.git",
	}
	for _, url := range urls {
		got := getRepoName(url)
		if got != want {
			t.Errorf("getRepoName(%q) == %q, want %q", url, got, want)
		}
	}
}
