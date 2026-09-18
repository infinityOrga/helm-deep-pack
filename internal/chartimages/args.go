package chartimages

import "strings"

func collectArgumentImageCandidates(value, nextValue string, hasNext bool) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	var candidates []string
	tokens := strings.FieldsFunc(value, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == ',' || r == ';'
	})
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if idx := strings.LastIndex(token, "="); idx >= 0 && idx+1 < len(token) {
			lhs := token[:idx]
			if isImageFlag(lhs) {
				candidates = append(candidates, token[idx+1:])
			}
			continue
		}
		lowerToken := strings.ToLower(token)
		if (lowerToken == "--set" || lowerToken == "--set-string") && i+1 < len(tokens) {
			candidates = append(candidates, collectSetAssignmentCandidates(tokens[i+1])...)
			i++
			continue
		}
		if strings.HasPrefix(token, "--") && isImageFlag(token) && i+1 < len(tokens) {
			candidates = appendImageFlagCandidate(candidates, token, tokens[i+1])
			i++
			continue
		}
	}

	if len(tokens) == 1 && hasNext {
		token := strings.TrimSpace(tokens[0])
		lowerToken := strings.ToLower(token)
		if lowerToken == "--set" || lowerToken == "--set-string" {
			candidates = append(candidates, collectSetAssignmentCandidates(nextValue)...)
		} else if strings.HasPrefix(token, "--") && isImageFlag(token) {
			candidates = appendImageFlagCandidate(candidates, token, nextValue)
		}
	}

	return candidates
}

func collectSetAssignmentCandidates(value string) []string {
	var candidates []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		idx := strings.Index(item, "=")
		if idx <= 0 || idx+1 >= len(item) {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(item[:idx]))
		if !strings.Contains(key, "image") {
			continue
		}
		candidates = append(candidates, strings.TrimSpace(item[idx+1:]))
	}
	return candidates
}

func appendImageFlagCandidate(candidates []string, token, value string) []string {
	token = strings.TrimSpace(token)
	if token == "" || !isImageFlag(token) {
		return candidates
	}
	return append(candidates, strings.TrimSpace(value))
}

func isImageFlag(token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	// Prometheus Operator calls its image flag --prometheus-config-reloader,
	// rather than including the word "image" in the flag name.
	return strings.Contains(token, "image") || strings.HasSuffix(token, "-config-reloader")
}
