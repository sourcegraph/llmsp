package providers

import "testing"

func TestGetRepoName(t *testing.T) {
	testCases := []struct {
		gitURL string
		want   string
	}{
		{
			gitURL: "git@github.com:sourcegraph/sourcegraph.git",
			want:   "github.com/sourcegraph/sourcegraph",
		},
		{
			gitURL: "https://github.com/sourcegraph/sourcegraph.git",
			want:   "github.com/sourcegraph/sourcegraph",
		},
		{
			gitURL: "git://github.com/sourcegraph/sourcegraph.git",
			want:   "github.com/sourcegraph/sourcegraph",
		},
		{
			gitURL: "http://github.com/sourcegraph/sourcegraph.git",
			want:   "github.com/sourcegraph/sourcegraph",
		},
		{
			gitURL: "https://username:password@github.com/sourcegraph/sourcegraph.git",
			want:   "github.com/sourcegraph/sourcegraph",
		},
	}

	for _, tc := range testCases {
		got := getRepoName(tc.gitURL)
		if got != tc.want {
			t.Errorf("getRepoName(%q) == %q, want %q", tc.gitURL, got, tc.want)
		}
	}
}
