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

func TestDetermineLanguage(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"./foo.go", "Go"},
		{"./bar.py", "Python"},
		{"./baz.js", "JavaScript"},
		{"./qux.ts", "TypeScript"},
		{"./quux.tsx", "TypeScript React"},
		{"./corge.java", "Java"},
		{"./grault.c", "C"},
		{"./garply.cpp", "C++"},
		{"./fred.lua", "Lua"},
		{"./plugh.rb", "Ruby"},
		{"./xyzzy.php", "PHP"},
		{"./thud.cs", "C#"},
		{"./foo.bar", "bar"},
		{"./foo.baz", "baz"},
		{"./foo.txt", "txt"},
		{"./foo.md", "md"},
	}

	for _, test := range tests {
		got := determineLanguage(test.filename)
		if got != test.want {
			t.Errorf("determineLanguage(%q) == %q, want %q", test.filename, got,
				test.want)
		}
	}
}
