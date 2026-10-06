package handlers

import (
	"net/mail"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxNewsletterSubscribeBody = 4 << 10
	maxSubjectLen              = 255
	maxPreviewLen              = 255
	maxNewsletterNameRunes     = 100
	maxNewsletterLists         = 20
)

// normalizeNewsletterEmail accepts only a bare, plain ASCII address and
// returns it lowercased. It is deliberately stricter than RFC 5322: the
// subscribe form is open to the internet, and every address it stores will
// one day be put in a To: header. Display names, comments, quoted local
// parts, whitespace, control characters (header injection) and non-ASCII
// domains (lookalike spoofing) are all refused.
func normalizeNewsletterEmail(raw string) (string, bool) {
	email := strings.TrimSpace(raw)
	if email == "" || len(email) > 254 {
		return "", false
	}
	for _, r := range email {
		if r > unicode.MaxASCII || r <= ' ' || r == 0x7f {
			return "", false
		}
	}
	if strings.Count(email, "@") != 1 {
		return "", false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Name != "" || addr.Address != email {
		return "", false
	}
	local, domain, _ := strings.Cut(email, "@")
	if !validLocalPart(local) || !validDomain(domain) {
		return "", false
	}
	return strings.ToLower(email), true
}

func validLocalPart(local string) bool {
	if local == "" || len(local) > 64 || local[0] == '.' || local[len(local)-1] == '.' || strings.Contains(local, "..") {
		return false
	}
	for _, r := range local {
		if !(isASCIIAlnum(r) || strings.ContainsRune("!#$%&'*+/=?^_`{|}~.-", r)) {
			return false
		}
	}
	return true
}

func validDomain(domain string) bool {
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !isASCIIAlnum(r) && r != '-' {
				return false
			}
		}
	}
	return true
}

func isASCIIAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// normalizeNewsletterName trims an optional display name. Empty is fine. It
// must be valid UTF-8, at most 100 characters, with no control characters
// (which includes line breaks: a name will end up in mail headers).
func normalizeNewsletterName(raw string) (string, bool) {
	if !utf8.ValidString(raw) {
		return "", false
	}
	name := strings.TrimSpace(raw)
	if utf8.RuneCountInString(name) > maxNewsletterNameRunes {
		return "", false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return name, true
}

// normalizeListIDs de-duplicates and sorts list ids, rejecting non-positive
// ids and counts outside [min, max] after de-duplication.
func normalizeListIDs(ids []int64, min, max int) ([]int64, bool) {
	seen := map[int64]bool{}
	out := []int64{}
	for _, id := range ids {
		if id <= 0 {
			return nil, false
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(out) < min || len(out) > max {
		return nil, false
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, true
}
