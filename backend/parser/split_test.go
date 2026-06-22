package parser

import "testing"

func TestSplitFrontmatter(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		fmWant   string
		bodyWant string
	}{
		{
			name:     "standard frontmatter",
			content:  "---\ntitle: Hello\ntags: [a, b]\n---\nbody line 1\nbody line 2",
			fmWant:   "---\ntitle: Hello\ntags: [a, b]\n---\n",
			bodyWant: "body line 1\nbody line 2",
		},
		{
			name:     "no frontmatter",
			content:  "just a body\nwith lines",
			fmWant:   "",
			bodyWant: "just a body\nwith lines",
		},
		{
			name:     "empty content",
			content:  "",
			fmWant:   "",
			bodyWant: "",
		},
		{
			name:     "opening only no closing",
			content:  "---\nnot really frontmatter\nbody here",
			fmWant:   "",
			bodyWant: "---\nnot really frontmatter\nbody here",
		},
		{
			name:     "frontmatter only no body",
			content:  "---\nkey: val\n---\n",
			fmWant:   "---\nkey: val\n---\n",
			bodyWant: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fm, body := SplitFrontmatter(c.content)
			if fm != c.fmWant {
				t.Errorf("frontmatter = %q, want %q", fm, c.fmWant)
			}
			if body != c.bodyWant {
				t.Errorf("body = %q, want %q", body, c.bodyWant)
			}
		})
	}
}
