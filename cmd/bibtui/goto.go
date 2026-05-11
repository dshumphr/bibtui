package main

import (
	"fmt"
	"strconv"
	"strings"
)

// parseGotoQuery splits raw goto input into a book-name query and optional
// chapter / verse numbers.  Examples:
//
//	"john"        → bookQuery="john", chapter=0, verse=0
//	"john 3"      → bookQuery="john", chapter=3, verse=0
//	"john 3:16"   → bookQuery="john", chapter=3, verse=16
//	"1 cor 13:1"  → bookQuery="1 cor", chapter=13, verse=1
func parseGotoQuery(input string) (bookQuery string, chapter, verse int) {
	tokens := strings.Fields(input)
	if len(tokens) == 0 {
		return "", 0, 0
	}
	last := tokens[len(tokens)-1]
	if len(tokens) > 1 {
		if ch, v, ok := parseChVerse(last); ok {
			return strings.Join(tokens[:len(tokens)-1], " "), ch, v
		}
	}
	return strings.Join(tokens, " "), 0, 0
}

func parseChVerse(s string) (chapter, verse int, ok bool) {
	if idx := strings.Index(s, ":"); idx >= 0 {
		ch, e1 := strconv.Atoi(s[:idx])
		v, e2 := strconv.Atoi(s[idx+1:])
		if e1 == nil && e2 == nil && ch > 0 {
			return ch, v, true
		}
		return 0, 0, false
	}
	n, err := strconv.Atoi(s)
	if err == nil && n > 0 {
		return n, 0, true
	}
	return 0, 0, false
}

// bookCandidates returns indices (into the global books slice) of books whose
// display name or slug has the given prefix, case-insensitive.
func bookCandidates(query string) []int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	var result []int
	for i, b := range books {
		if strings.HasPrefix(strings.ToLower(b.name), q) ||
			strings.HasPrefix(b.slug, q) {
			result = append(result, i)
		}
	}
	return result
}

// findIndexPos returns the position in index for the given bookIdx (into
// books[]) and chapter number.  chapter ≤ 0 means "first chapter of the
// book".  Returns -1 if not found.
func findIndexPos(index []ref, bookIdx, chapter int) int {
	if bookIdx < 0 || bookIdx >= len(books) {
		return -1
	}
	target := books[bookIdx]
	for i, r := range index {
		if r.book.slug == target.slug {
			if chapter <= 0 || r.num == chapter {
				return i
			}
		}
	}
	return -1
}

// gotoCompleteInput replaces the book-name portion of input with the full
// display name of the chosen candidate, preserving any chapter/verse suffix.
func gotoCompleteInput(input string, bookName string) string {
	_, chapter, verse := parseGotoQuery(input)
	switch {
	case chapter > 0 && verse > 0:
		return fmt.Sprintf("%s %d:%d", bookName, chapter, verse)
	case chapter > 0:
		return fmt.Sprintf("%s %d", bookName, chapter)
	default:
		return bookName + " "
	}
}
