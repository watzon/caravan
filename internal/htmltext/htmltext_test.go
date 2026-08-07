package htmltext

import "testing"

func TestStrip(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "inline tags are dropped, breaks become blank lines",
			in:   "A boy <i>and</i> his titan.<br><br>(Source: Anime News Network)",
			want: "A boy and his titan.\n\n(Source: Anime News Network)",
		},
		{
			name: "paragraph markup does not glue sentences together",
			in:   "<p>First.</p><p>Second.</p>",
			want: "First.\n\nSecond.",
		},
		{
			// TVmaze's summary is a real HTML document fragment rather than
			// prose with the odd tag in it, so its paragraphs are the whole
			// structure and losing them would leave one run-on sentence.
			name: "a tvmaze summary keeps its paragraphs apart",
			in:   `<p><b>Breaking Bad</b> follows Walter White, a chemistry teacher.</p><p>Diagnosed with cancer, he turns to a life of crime.</p>`,
			want: "Breaking Bad follows Walter White, a chemistry teacher.\n\nDiagnosed with cancer, he turns to a life of crime.",
		},
		{
			// The common case must be a pass-through. A trailing newline here
			// would end up in every NFO written from a plain-text description.
			name: "text with no markup at all is unchanged",
			in:   "A chemistry teacher turns to crime.",
			want: "A chemistry teacher turns to crime.",
		},
		{
			name: "self-closing and attributed tags",
			in:   `Line one.<br />Line two.<a href="https://example.test">link</a>`,
			want: "Line one.\nLine two.link",
		},
		{
			name: "entities are unescaped",
			in:   "Kaguya&mdash;sama said &quot;no&quot;.",
			want: "Kaguya—sama said \"no\".",
		},
		{
			name: "escaped markup stays text",
			in:   "The tag &lt;i&gt; is italic.",
			want: "The tag <i> is italic.",
		},
		{
			name: "an unterminated angle bracket is prose",
			in:   "5 < 6 and that is that",
			want: "5 < 6 and that is that",
		},
		{
			name: "no description at all",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Strip(tt.in); got != tt.want {
				t.Errorf("Strip(%q) =\n %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}
