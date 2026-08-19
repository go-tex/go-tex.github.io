// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"unicode"

	"github.com/go-widgets/toolkit"
)

// latexWordChar is the CodeEditor.CompletionWordChar predicate for LaTeX: a
// "word" the completion popup filters on may start with a backslash (a control
// sequence like \section), and beyond the usual letters admits '@' so an
// internal macro (\makeatletter territory) reads as one word too. Digits are
// deliberately excluded — a LaTeX control word is letters only, so stopping the
// word at the first digit keeps "\section2" filtering as "\section". This is the
// rule that lets typing "\se" be a single word the widget narrows the list with.
func latexWordChar(r rune) bool {
	return r == '\\' || r == '@' || unicode.IsLetter(r)
}

// installCompletion wires the LaTeX autocompletion into the editor: the
// backslash-aware word rule above and a static candidate source. The widget owns
// the popup, the prefix filtering, the keyboard/mouse handling and the snippet
// (`$0`) caret placement — the playground only supplies the curated list, so the
// whole feature is these two field assignments.
func (s *State) installCompletion() {
	s.editor.CompletionWordChar = latexWordChar
	// The list is prefix-filtered by the widget against the word before the
	// caret, so returning the full curated set every call is correct (and cheap —
	// it is a single shared slice, built once). The doc/line/col context is
	// unused: a static list needs no cursor awareness.
	s.editor.CompletionSource = func(_ []string, _, _ int) []toolkit.CompletionItem {
		return latexCompletions
	}
}

// latexCompletions is the curated LaTeX candidate list the playground offers,
// built once at init: control sequences (with snippet insert-text carrying a $0
// caret stop), \begin{…} environment templates, and math symbols. It is grouped
// by purpose for readability; the widget re-orders nothing, so the display order
// within a filtered prefix matches this slice.
var latexCompletions = buildLatexCompletions()

// cmd builds a command candidate: Label is the control sequence, Detail its
// category, insert its snippet body (empty falls back to Label). Kind marks it a
// function so the popup's glyph column reads "fn".
func cmd(label, detail, insert string) toolkit.CompletionItem {
	return toolkit.CompletionItem{Label: label, Detail: detail, InsertText: insert, Kind: toolkit.CompletionFunction}
}

// kw builds a keyword candidate — a bare control sequence that takes no argument
// (so no snippet), drawn with the "kw" glyph.
func kw(label, detail string) toolkit.CompletionItem {
	return toolkit.CompletionItem{Label: label, Detail: detail, Kind: toolkit.CompletionKeyword}
}

// env builds a \begin{name}…\end{name} environment template: the Label reads
// \begin{name}, the snippet expands the whole block with the caret ($0) parked on
// the first content line, and the Snippet kind draws the "sn" glyph.
func env(name, body string) toolkit.CompletionItem {
	return toolkit.CompletionItem{
		Label:      "\\begin{" + name + "}",
		Detail:     "environment",
		InsertText: body,
		Kind:       toolkit.CompletionSnippet,
	}
}

// sym builds a math-symbol candidate (Constant kind, "cn" glyph), inserted
// verbatim (Label is the insert text).
func sym(label string) toolkit.CompletionItem {
	return toolkit.CompletionItem{Label: label, Detail: "math symbol", Kind: toolkit.CompletionConstant}
}

// buildLatexCompletions assembles the candidate slice. Kept a function (not a
// composite literal) so each group is a readable block and the ordering is
// explicit.
func buildLatexCompletions() []toolkit.CompletionItem {
	items := []toolkit.CompletionItem{
		// Sectioning.
		cmd("\\section", "sectioning", "\\section{$0}"),
		cmd("\\subsection", "sectioning", "\\subsection{$0}"),
		cmd("\\subsubsection", "sectioning", "\\subsubsection{$0}"),
		cmd("\\paragraph", "sectioning", "\\paragraph{$0}"),
		cmd("\\subparagraph", "sectioning", "\\subparagraph{$0}"),
		cmd("\\chapter", "sectioning", "\\chapter{$0}"),
		cmd("\\part", "sectioning", "\\part{$0}"),

		// Fonts / text.
		cmd("\\textbf", "font", "\\textbf{$0}"),
		cmd("\\textit", "font", "\\textit{$0}"),
		cmd("\\emph", "font", "\\emph{$0}"),
		cmd("\\texttt", "font", "\\texttt{$0}"),
		cmd("\\textsf", "font", "\\textsf{$0}"),
		cmd("\\textrm", "font", "\\textrm{$0}"),
		cmd("\\textsc", "font", "\\textsc{$0}"),
		cmd("\\underline", "font", "\\underline{$0}"),
		cmd("\\text", "math", "\\text{$0}"),

		// Structure / lists.
		cmd("\\item", "list item", "\\item $0"),
		kw("\\maketitle", "title block"),
		kw("\\tableofcontents", "toc"),
		kw("\\newpage", "break"),
		kw("\\clearpage", "break"),
		kw("\\noindent", "spacing"),
		kw("\\centering", "alignment"),

		// References / citations / notes.
		cmd("\\label", "reference", "\\label{$0}"),
		cmd("\\ref", "reference", "\\ref{$0}"),
		cmd("\\pageref", "reference", "\\pageref{$0}"),
		cmd("\\eqref", "reference", "\\eqref{$0}"),
		cmd("\\cite", "citation", "\\cite{$0}"),
		cmd("\\footnote", "note", "\\footnote{$0}"),
		cmd("\\caption", "float", "\\caption{$0}"),
		cmd("\\href", "link", "\\href{$0}{}"),
		cmd("\\url", "link", "\\url{$0}"),
		cmd("\\includegraphics", "graphics", "\\includegraphics[]{$0}"),

		// Preamble.
		cmd("\\documentclass", "preamble", "\\documentclass{$0}"),
		cmd("\\usepackage", "preamble", "\\usepackage{$0}"),
		cmd("\\title", "preamble", "\\title{$0}"),
		cmd("\\author", "preamble", "\\author{$0}"),
		cmd("\\date", "preamble", "\\date{$0}"),
		cmd("\\newcommand", "macro", "\\newcommand{$0}{}"),
		cmd("\\renewcommand", "macro", "\\renewcommand{$0}{}"),
		cmd("\\setlength", "length", "\\setlength{$0}{}"),
		cmd("\\setcounter", "counter", "\\setcounter{$0}{}"),

		// Math constructs.
		cmd("\\frac", "math", "\\frac{$0}{}"),
		cmd("\\dfrac", "math", "\\dfrac{$0}{}"),
		cmd("\\sqrt", "math", "\\sqrt{$0}"),
		cmd("\\sum", "math", "\\sum_{$0}^{}"),
		cmd("\\prod", "math", "\\prod_{$0}^{}"),
		cmd("\\int", "math", "\\int_{$0}^{}"),
		cmd("\\lim", "math", "\\lim_{$0}"),
		cmd("\\overline", "math", "\\overline{$0}"),
		cmd("\\vec", "math", "\\vec{$0}"),
		cmd("\\hat", "math", "\\hat{$0}"),
		kw("\\left", "math delimiter"),
		kw("\\right", "math delimiter"),
		cmd("\\mathbb", "math font", "\\mathbb{$0}"),
		cmd("\\mathcal", "math font", "\\mathcal{$0}"),
		cmd("\\mathbf", "math font", "\\mathbf{$0}"),
		cmd("\\mathrm", "math font", "\\mathrm{$0}"),

		// Generic begin/end (the environment templates below are the fast path).
		cmd("\\begin", "environment", "\\begin{$0}"),
		cmd("\\end", "environment", "\\end{$0}"),
	}

	// Environment templates: \begin{…}…\end{…} in one accept.
	items = append(items,
		env("itemize", "\\begin{itemize}\n\t\\item $0\n\\end{itemize}"),
		env("enumerate", "\\begin{enumerate}\n\t\\item $0\n\\end{enumerate}"),
		env("description", "\\begin{description}\n\t\\item[$0] \n\\end{description}"),
		env("equation", "\\begin{equation}\n\t$0\n\\end{equation}"),
		env("align", "\\begin{align}\n\t$0\n\\end{align}"),
		env("figure", "\\begin{figure}\n\t\\centering\n\t$0\n\\end{figure}"),
		env("table", "\\begin{table}\n\t\\centering\n\t$0\n\\end{table}"),
		env("tabular", "\\begin{tabular}{$0}\n\\end{tabular}"),
		env("verbatim", "\\begin{verbatim}\n$0\n\\end{verbatim}"),
		env("center", "\\begin{center}\n\t$0\n\\end{center}"),
		env("quote", "\\begin{quote}\n\t$0\n\\end{quote}"),
		env("abstract", "\\begin{abstract}\n\t$0\n\\end{abstract}"),
		env("matrix", "\\begin{matrix}\n\t$0\n\\end{matrix}"),
	)

	// Math symbols (Greek, operators, relations, sets, arrows, calculus).
	for _, sy := range []string{
		"\\alpha", "\\beta", "\\gamma", "\\delta", "\\epsilon", "\\varepsilon",
		"\\zeta", "\\eta", "\\theta", "\\vartheta", "\\iota", "\\kappa",
		"\\lambda", "\\mu", "\\nu", "\\xi", "\\pi", "\\rho", "\\sigma",
		"\\tau", "\\phi", "\\varphi", "\\chi", "\\psi", "\\omega",
		"\\Gamma", "\\Delta", "\\Theta", "\\Lambda", "\\Pi", "\\Sigma",
		"\\Phi", "\\Psi", "\\Omega",
		"\\infty", "\\partial", "\\nabla", "\\times", "\\cdot", "\\pm", "\\mp",
		"\\leq", "\\geq", "\\neq", "\\approx", "\\equiv", "\\sim", "\\propto",
		"\\rightarrow", "\\leftarrow", "\\Rightarrow", "\\Leftarrow",
		"\\leftrightarrow", "\\mapsto",
		"\\forall", "\\exists", "\\nexists", "\\in", "\\notin", "\\subset",
		"\\subseteq", "\\supset", "\\cup", "\\cap", "\\emptyset", "\\setminus",
		"\\langle", "\\rangle", "\\lfloor", "\\rfloor",
	} {
		items = append(items, sym(sy))
	}

	return items
}
