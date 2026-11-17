package tui

import (
	"net/url"
	"sort"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	gstyles "charm.land/glamour/v2/styles"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Glamour prints a link as its text followed by its URL, which doubles the width of
// every link and breaks the URL across lines in a narrow pane. There is no option to
// drop the URL, so inline links are rewritten before rendering: the destination
// becomes "#" (Glamour prints nothing for an anchor) and the link is bracketed by two
// private-use runes. After rendering, those runes become an OSC 8 hyperlink around
// the styled text, so the text stays clickable in terminals that support it.
const (
	linkOpenBase = '' // linkOpenBase+n opens the n-th link
	linkClose    = ''
	maxLinks     = int(linkClose - linkOpenBase)
)

var (
	linkSpansKey = parser.NewContextKey()
	linkOpensKey = parser.NewContextKey()
	linkParserMD = newLinkParserMD()
)

// linkSpan locates one inline link in the source, as byte offsets.
type linkSpan struct {
	open  int // the "[" that starts the text
	close int // the "]" that ends the text
	end   int // first byte after the destination
	url   string
}

// linkRecorder wraps goldmark's link parser and records where each link sits.
type linkRecorder struct{ inner parser.InlineParser }

func (r linkRecorder) Trigger() []byte { return r.inner.Trigger() }

func (r linkRecorder) Parse(parent gast.Node, block text.Reader, pc parser.Context) gast.Node {
	line, seg := block.PeekLine()
	var first, second byte
	if len(line) > 0 {
		first = line[0]
	}
	if len(line) > 1 {
		second = line[1]
	}
	node := r.inner.Parse(parent, block, pc)

	opens, _ := pc.Get(linkOpensKey).([]int)
	switch {
	case first == '[':
		pc.Set(linkOpensKey, append(opens, seg.Start))
	case first == '!' && second == '[':
		pc.Set(linkOpensKey, append(opens, seg.Start+1))
	case first == ']' && len(opens) > 0:
		open := opens[len(opens)-1]
		pc.Set(linkOpensKey, opens[:len(opens)-1])
		if link, ok := node.(*gast.Link); ok {
			_, pos := block.Position()
			spans, _ := pc.Get(linkSpansKey).([]linkSpan)
			pc.Set(linkSpansKey, append(spans, linkSpan{open: open, close: seg.Start, end: pos.Start, url: string(link.Destination)}))
		}
	}
	return node
}

func newLinkParserMD() goldmark.Markdown {
	inline := parser.DefaultInlineParsers()
	for i, p := range inline {
		if ip, ok := p.Value.(parser.InlineParser); ok && strings.Contains(string(ip.Trigger()), "]") {
			inline[i] = util.Prioritized(linkRecorder{inner: ip}, p.Priority)
		}
	}
	return goldmark.New(
		goldmark.WithParser(parser.NewParser(
			parser.WithBlockParsers(parser.DefaultBlockParsers()...),
			parser.WithInlineParsers(inline...),
			parser.WithParagraphTransformers(parser.DefaultParagraphTransformers()...),
		)),
		goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
	)
}

// foldLinks rewrites inline and reference links to text-only anchors and returns the
// destination of each marked link, indexed by the marker it was given.
func foldLinks(src string) (string, []string) {
	ctx := parser.NewContext()
	linkParserMD.Parser().Parse(text.NewReader([]byte(src)), parser.WithContext(ctx))
	spans, _ := ctx.Get(linkSpansKey).([]linkSpan)
	if len(spans) == 0 {
		return src, nil
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].open < spans[j].open })

	var (
		b    strings.Builder
		urls []string
		pos  int
	)
	for _, s := range spans {
		if s.open < pos || s.end > len(src) || s.close >= s.end {
			continue
		}
		b.WriteString(src[pos:s.open])
		marked := len(urls) < maxLinks && hasScheme(s.url)
		if marked {
			b.WriteRune(linkOpenBase + rune(len(urls)))
			urls = append(urls, s.url)
		}
		b.WriteString(src[s.open : s.close+1])
		b.WriteString("(#)")
		if marked {
			b.WriteRune(linkClose)
		}
		pos = s.end
	}
	b.WriteString(src[pos:])
	return b.String(), urls
}

func hasScheme(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme != ""
}

// unfoldLinks turns the markers left by foldLinks into OSC 8 hyperlinks, or drops them
// when links are disabled. Each line closes and reopens the link so a line cut later
// never leaves one open.
func unfoldLinks(out string, urls []string, hyperlinks bool) string {
	if len(urls) == 0 {
		return out
	}
	var b strings.Builder
	current := ""
	for _, r := range out {
		switch {
		case r == linkClose:
			if current != "" {
				b.WriteString(xansi.ResetHyperlink())
				current = ""
			}
		case r >= linkOpenBase && r < linkClose:
			if hyperlinks && int(r-linkOpenBase) < len(urls) {
				current = urls[r-linkOpenBase]
				b.WriteString(xansi.SetHyperlink(current))
			}
		case r == '\n' && current != "":
			b.WriteString(xansi.ResetHyperlink())
			b.WriteRune(r)
			b.WriteString(xansi.SetHyperlink(current))
		default:
			b.WriteRune(r)
		}
	}
	if current != "" {
		b.WriteString(xansi.ResetHyperlink())
	}
	return b.String()
}

// stripMarkers removes private-use runes from user text so it cannot forge a link marker.
func stripMarkers(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r >= linkOpenBase && r <= linkClose }) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r >= linkOpenBase && r <= linkClose {
			return -1
		}
		return r
	}, s)
}

// markdownStyle derives Glamour's dark, light or no-color style, dropping the "#"
// prefixes of headings; they stay apart by color and weight.
func markdownStyle(e env) ansi.StyleConfig {
	var cfg ansi.StyleConfig
	switch {
	case e.noColor:
		cfg = gstyles.NoTTYStyleConfig
	case !e.dark:
		cfg = gstyles.LightStyleConfig
	default:
		cfg = gstyles.DarkStyleConfig
	}
	bold := true
	for _, h := range []*ansi.StyleBlock{&cfg.H1, &cfg.H2, &cfg.H3, &cfg.H4, &cfg.H5, &cfg.H6} {
		h.Prefix, h.Suffix = "", ""
		if e.noColor {
			h.Bold = &bold
		}
	}
	return cfg
}

// markdown renders src with Glamour, falling back to the raw text if rendering fails.
// Single newlines inside a paragraph are kept, as Redmine's CommonMark formatter does,
// and links show only their text.
func markdown(e env, src string, width int) string {
	folded, urls := foldLinks(stripMarkers(src))
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(markdownStyle(e)),
		glamour.WithWordWrap(max(20, width)),
		glamour.WithPreservedNewLines(),
		glamour.WithInlineTableLinks(true),
	)
	if err != nil {
		return src
	}
	out, err := r.Render(folded)
	if err != nil {
		return src
	}
	return strings.Trim(unfoldLinks(out, urls, !e.noColor), "\n")
}
