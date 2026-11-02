package main

import (
	"fmt"
	"html"
	"path"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// Code blocks are highlighted here rather than by goldmark-highlighting so that:
//   - every token is emitted as a short chroma class (".nt", ".s2", ...) styled by the site's own
//     palette in static/shared.css, with no chroma theme involved;
//   - shell blocks get a second pass that marks kuma-migrator commands (k-bin, k-sub, k-flag,
//     k-val) and kubectl/kumactl lines (k-manual), the same classes as the landing page;
//   - plain-output ("text") blocks get the CLI's status glyphs coloured (⚠ ✗ ✓).

// codeLang classifies a fence language tag.
type codeLang int

const (
	langOther   codeLang = iota // any other chroma lexer
	langText                    // plain output: no lexer, status-glyph pass only
	langShell                   // bash/sh/shell/zsh: chroma bash lexer + command pass
	langConsole                 // prompt + output: chroma bash-session lexer + command pass
)

func classifyLang(lang string) codeLang {
	switch strings.ToLower(lang) {
	case "text", "plain", "plaintext", "txt":
		return langText
	case "bash", "sh", "shell", "zsh":
		return langShell
	case "console", "shell-session", "bash-session":
		return langConsole
	}
	return langOther
}

// checkLang returns an error message for a fence language the generator cannot highlight.
func checkLang(lang string) string {
	if lang == "" {
		return "code block without a language tag: add one (yaml, bash, console, json, go, markdown, ...) or `text` for plain output"
	}
	if classifyLang(lang) != langOther {
		return ""
	}
	if lexers.Get(lang) == nil {
		return fmt.Sprintf("code block language %q is not known to chroma", lang)
	}
	return ""
}

// codeRenderer renders fenced and indented code blocks.
type codeRenderer struct{}

func (codeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, renderCode)
	reg.Register(ast.KindCodeBlock, renderCode)
}

func newCodeRenderer() util.PrioritizedValue { return util.Prioritized(codeRenderer{}, 100) }

func renderCode(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	lang := ""
	if f, ok := n.(*ast.FencedCodeBlock); ok {
		lang = string(f.Language(src))
	}
	var code strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		code.Write(seg.Value(src))
	}
	_, _ = w.WriteString(highlight(lang, code.String()))
	return ast.WalkSkipChildren, nil
}

// seg is one run of output text and the class it is wrapped in ("" = no span).
type seg struct{ cls, text string }

// highlight renders one code block, wrapper included.
func highlight(lang, code string) string {
	var segs []seg
	switch kind := classifyLang(lang); kind {
	case langText:
		segs = outputSegments(code)
	default:
		lexer := lexers.Get(lang)
		if lang == "" || lexer == nil {
			segs = []seg{{"", code}}
			break
		}
		it, err := lexer.Tokenise(nil, code)
		if err != nil {
			segs = []seg{{"", code}}
			break
		}
		toks := it.Tokens()
		if kind == langShell || kind == langConsole {
			segs = shellSegments(toks)
		} else {
			for _, t := range toks {
				segs = append(segs, seg{tokenClass(t.Type), t.Value})
			}
		}
	}
	var b strings.Builder
	b.WriteString(`<div class="code"><pre class="chroma"`)
	if lang != "" {
		b.WriteString(` data-lang="` + html.EscapeString(strings.ToLower(lang)) + `"`)
	}
	b.WriteString("><code>")
	writeSegs(&b, segs)
	b.WriteString("</code></pre></div>\n")
	return b.String()
}

// codeEscaper escapes text content; quotes need no escaping outside attributes.
var codeEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// writeSegs writes segments, merging neighbours with the same class.
func writeSegs(b *strings.Builder, segs []seg) {
	for i := 0; i < len(segs); {
		cls := segs[i].cls
		var text strings.Builder
		for ; i < len(segs) && segs[i].cls == cls; i++ {
			text.WriteString(segs[i].text)
		}
		if text.Len() == 0 {
			continue
		}
		if cls == "" {
			b.WriteString(codeEscaper.Replace(text.String()))
		} else {
			fmt.Fprintf(b, `<span class="%s">%s</span>`, cls, codeEscaper.Replace(text.String()))
		}
	}
}

// tokenClass maps a chroma token type to its short CSS class, falling back to the parent
// category. Plain text and whitespace get no class.
func tokenClass(t chroma.TokenType) string {
	switch t {
	case chroma.Text, chroma.TextWhitespace, chroma.Background, chroma.Error:
		return ""
	}
	for _, tt := range []chroma.TokenType{t, t.SubCategory(), t.Category()} {
		if c, ok := chroma.StandardTypes[tt]; ok && c != "" {
			return c
		}
	}
	return ""
}

// ---- shell command pass ----

// boolFlags take no value, so the word after them is not styled as a value.
var boolFlags = map[string]bool{
	"--dry-run": true, "--tls-skip-verify": true, "-k": true, "--help": true, "-h": true,
	"--version": true, "--clean": true, "--all-namespaces": true, "-A": true, "--force": true,
	"-L": true, "-s": true, "-S": true, "-fsSL": true, "-sSL": true, "--recursive": true,
}

type shellState struct {
	start       bool   // the next word is a command name
	cmd         string // "kuma-migrator", "kube" (kubectl/kumactl) or "other"
	sub         bool   // kuma-migrator subcommand already seen
	expectValue bool   // the previous word was a flag that takes a value
	afterAssign bool   // directly after VAR=
}

func (st *shellState) reset() { *st = shellState{start: true} }

// word classifies one whitespace-free word of shell text.
func (st *shellState) word(w string) []seg {
	if st.afterAssign {
		st.afterAssign = false
		return []seg{{"", w}}
	}
	if st.start {
		st.start, st.sub, st.expectValue = false, false, false
		switch path.Base(w) {
		case "kuma-migrator":
			st.cmd = "kuma-migrator"
			return []seg{{"k-bin", w}}
		case "kubectl", "kumactl":
			st.cmd = "kube"
			return []seg{{"k-manual", w}}
		}
		st.cmd = "other"
		return []seg{{"k-cmd", w}}
	}
	if len(w) > 1 && w[0] == '-' && w != "--" {
		st.expectValue = false
		if i := strings.IndexByte(w, '='); i > 0 {
			return []seg{{"k-flag", w[:i]}, {"o", "="}, {"k-val", w[i+1:]}}
		}
		st.expectValue = !boolFlags[w]
		return []seg{{"k-flag", w}}
	}
	if st.expectValue {
		st.expectValue = false
		return []seg{{"k-val", w}}
	}
	switch st.cmd {
	case "kuma-migrator":
		if !st.sub {
			st.sub = true
			return []seg{{"k-sub", w}}
		}
		return []seg{{"k-val", w}}
	case "kube":
		return []seg{{"k-manual-b", w}}
	}
	return []seg{{"", w}}
}

var wsRe = regexp.MustCompile(`\s+|\S+`)

// shellSegments re-classifies the words of a chroma bash (or bash-session) token stream.
// Comments, strings, variables and operators keep chroma's classes; bare words become
// command names, subcommands, flags and flag values.
func shellSegments(toks []chroma.Token) []seg {
	var out []seg
	st := &shellState{start: true}
	for _, t := range toks {
		cls := tokenClass(t.Type)
		switch {
		case t.Type == chroma.GenericPrompt:
			st.reset()
			out = append(out, seg{cls, t.Value})
		case t.Type == chroma.GenericOutput:
			st.reset()
			out = append(out, outputSegments(t.Value)...)
		case t.Type == chroma.Text || t.Type == chroma.TextWhitespace:
			for _, part := range wsRe.FindAllString(t.Value, -1) {
				if strings.TrimSpace(part) == "" {
					if strings.Contains(part, "\n") {
						st.reset()
					}
					out = append(out, seg{"", part})
					continue
				}
				out = append(out, st.word(part)...)
			}
		case t.Type == chroma.LiteralStringEscape && strings.HasPrefix(t.Value, "\\") && strings.TrimSpace(t.Value[1:]) == "":
			out = append(out, seg{"se", t.Value}) // line continuation: same command
		case t.Type == chroma.NameBuiltin && st.start:
			st.start, st.cmd = false, "other"
			out = append(out, seg{"k-cmd", t.Value})
		case t.Value == "&&" || t.Value == "||" || t.Value == ";" || t.Value == "|" || t.Value == "$(" || t.Value == "(" || t.Value == "`":
			st.reset()
			out = append(out, seg{cls, t.Value})
		case t.Type == chroma.NameVariable && st.start:
			// VAR=value prefix: the command is still to come.
			out = append(out, seg{cls, t.Value})
		case t.Type == chroma.Operator && t.Value == "=":
			// --flag=value keeps expecting the value; NAME=value is an assignment.
			st.afterAssign = !st.expectValue
			out = append(out, seg{cls, t.Value})
		case t.Type.InCategory(chroma.LiteralString):
			st.afterAssign = false
			if st.start {
				st.start, st.cmd = false, "other"
			}
			st.expectValue = false
			out = append(out, seg{cls, t.Value})
		default:
			if !t.Type.InCategory(chroma.Comment) {
				st.afterAssign, st.expectValue = false, false
			}
			out = append(out, seg{cls, t.Value})
		}
	}
	return out
}

// ---- plain output pass ----

var (
	outputLineRe = regexp.MustCompile(`^(\s*)(⚠|✗|✓|●|–|\[WARN\]|\[INFO\]|\[ERROR\]|Error:)`)
	// "←" starts an annotation anywhere ("dir/   ← what it holds", "←  input path");
	// "→" only when it starts the line ("→  output path"), not in "a → b" prose.
	outputArrow = regexp.MustCompile(`^\s*([←→]\s)|\s(←\s)`)
)

var outputClass = map[string]string{
	"⚠": "o-warn", "[WARN]": "o-warn",
	"✗": "o-err", "[ERROR]": "o-err", "Error:": "o-err",
	"✓": "o-ok",
	"●": "o-info", "[INFO]": "o-info",
	"–": "o-dim",
}

// outputSegments colours CLI output: the status glyph that starts a line, and the
// "← input / → output" path annotations (dimmed, as the CLI prints them).
func outputSegments(code string) []seg {
	var out []seg
	for _, line := range strings.SplitAfter(code, "\n") {
		rest := line
		if m := outputLineRe.FindStringSubmatch(rest); m != nil {
			out = append(out, seg{"", m[1]}, seg{outputClass[m[2]], m[2]})
			rest = rest[len(m[0]):]
		}
		if m := outputArrow.FindStringSubmatchIndex(rest); m != nil {
			start := m[2]
			if start < 0 {
				start = m[4]
			}
			end := len(strings.TrimRight(rest, "\n"))
			out = append(out, seg{"", rest[:start]}, seg{"o-dim", rest[start:end]}, seg{"", rest[end:]})
			continue
		}
		out = append(out, seg{"", rest})
	}
	return out
}
