package dsml

import "strings"

// RepairCompletion repairs a completion whose DSML tool-calls block lost only
// its closing wrapper to the token limit, mirroring upstream ds4's
// try_repair_dsml. Every invoke and parameter must already be closed: a
// truncated value may be a partial shell command, and closing it would invent
// an executable action, so those completions are left for the caller to
// report as incomplete. Near-miss markers are normalized first so a sampled
// typo and a truncation in the same block both recover, and the returned text
// is the normalized form.
//
// Tags before the last structural </think> (one outside any parameter body)
// are not counted: DSML quoted inside reasoning is not executable and would
// inflate the counts into false repairs. Repair is refused when any closing
// tag outnumbers its opener — extra closers are not a truncation pattern.
//
// It returns the repaired text and true when a repair was applied, or the
// input unchanged and false otherwise. Callers should re-parse the repaired
// text and keep their original result if the re-parse still yields no calls.
func RepairCompletion(text string) (string, bool) {
	return RepairCompletionSyntax(SyntaxDSML, text)
}

// RepairCompletionSyntax is [RepairCompletion] for an explicit DSML dialect.
// GLM has no wrapper block to repair and is returned unchanged.
func RepairCompletionSyntax(syntax Syntax, text string) (string, bool) {
	if syntax == SyntaxGLM {
		return text, false
	}
	normalized := normalizeMarkers(text)
	scanFrom := 0
	if end := lastStructuralIndex(normalized, thinkingEndToken); end >= 0 {
		scanFrom = end + len(thinkingEndToken)
	}
	scan := normalized[scanFrom:]

	// Dialect detection follows upstream's priority order (fullwidth, short,
	// plain) rather than first occurrence; dsmlSyntaxes is in that order.
	var syn dsmlSyntax
	found := false
	for _, candidate := range syntaxTable(syntax) {
		if strings.Contains(scan, candidate.toolStart) {
			syn = candidate
			found = true
			break
		}
	}
	if !found {
		return text, false
	}

	toolOpen := strings.Count(scan, syn.toolStart)
	toolClose := strings.Count(scan, syn.toolEnd)
	invokeOpen := strings.Count(scan, syn.invokeStart)
	invokeClose := strings.Count(scan, syn.invokeEnd)
	paramOpen := strings.Count(scan, syn.paramStart)
	paramClose := strings.Count(scan, syn.paramEnd)

	// Only the enclosing wrapper is ever supplied. A truncated parameter or
	// invoke is left alone: a closed value may still be a truncated shell
	// command, and closing it would invent an executable action.
	if invokeOpen != invokeClose || paramOpen != paramClose {
		return text, false
	}
	if toolOpen == toolClose {
		return text, false
	}
	if toolClose > toolOpen {
		return text, false
	}

	var b strings.Builder
	b.WriteString(normalized)
	for i := 0; i < toolOpen-toolClose; i++ {
		b.WriteString(syn.toolEnd)
	}
	return b.String(), true
}
