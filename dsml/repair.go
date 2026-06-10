package dsml

import "strings"

// RepairCompletion repairs a completion whose DSML tool-calls block was
// truncated by the token limit, mirroring upstream ds4's try_repair_dsml.
// Generation that stops mid-stanza leaves unclosed parameter/invoke/tool_calls
// tags; appending the missing closers in reverse nesting order turns the
// truncated suffix back into a parseable block. Near-miss markers are
// normalized first so a sampled typo and a truncation in the same block both
// recover, and the returned text is the normalized form.
//
// Tags before the last </think> are not counted: DSML quoted inside reasoning
// is not executable and would inflate the counts into false repairs. Repair is
// refused when any closing tag outnumbers its opener — extra closers are not a
// truncation pattern.
//
// It returns the repaired text and true when a repair was applied, or the
// input unchanged and false otherwise. Callers should re-parse the repaired
// text and keep their original result if the re-parse still yields no calls.
func RepairCompletion(text string) (string, bool) {
	normalized := normalizeMarkers(text)
	scanFrom := 0
	if end := strings.LastIndex(normalized, thinkingEndToken); end >= 0 {
		scanFrom = end + len(thinkingEndToken)
	}
	scan := normalized[scanFrom:]

	// Dialect detection follows upstream's priority order (fullwidth, short,
	// plain) rather than first occurrence; dsmlSyntaxes is in that order.
	var syn dsmlSyntax
	found := false
	for _, candidate := range dsmlSyntaxes {
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

	if toolOpen == toolClose && invokeOpen == invokeClose && paramOpen == paramClose {
		return text, false
	}
	if toolClose > toolOpen || invokeClose > invokeOpen || paramClose > paramOpen {
		return text, false
	}

	var b strings.Builder
	b.WriteString(normalized)
	for i := 0; i < paramOpen-paramClose; i++ {
		b.WriteString(syn.paramEnd)
	}
	for i := 0; i < invokeOpen-invokeClose; i++ {
		b.WriteString(syn.invokeEnd)
	}
	for i := 0; i < toolOpen-toolClose; i++ {
		b.WriteString(syn.toolEnd)
	}
	return b.String(), true
}
