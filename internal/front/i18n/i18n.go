// Package i18n holds the words zellij-reaper shows people, in English and
// Korean. English is the default; Korean is used when the locale says so
// (LC_ALL, LC_MESSAGES or LANG starting with "ko"), or when
// ZELLIJ_REAPER_LANG is "ko". The log is always English, so it can be grepped
// and shared the same way everywhere.
package i18n

import (
	"fmt"
	"os"
	"strings"
)

// Lang is a language code: "en" or "ko".
type Lang string

const (
	// English is the default.
	English Lang = "en"
	// Korean is used for a Korean locale.
	Korean Lang = "ko"
)

// current is the language in use; Detect sets it at startup.
var current = English

// Detect picks the language from the environment. ZELLIJ_REAPER_LANG wins,
// then the POSIX order LC_ALL, LC_MESSAGES, LANG.
func Detect(lookup func(string) (string, bool)) Lang {
	for _, k := range []string{"ZELLIJ_REAPER_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		v, ok := lookup(k)
		if !ok || v == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(v), "ko") {
			return Korean
		}
		// The first variable that is set decides, as setlocale(3) does.
		return English
	}
	return English
}

// Init detects the language from the process environment.
func Init() { current = Detect(os.LookupEnv) }

// Set forces a language (tests, --lang).
func Set(l Lang) {
	if l == Korean {
		current = Korean
	} else {
		current = English
	}
}

// T returns the text for key in the current language, formatted with args. A
// key missing from Korean falls back to English; a key missing from both is
// shown as itself, which the catalog test turns into a failure.
func T(key string, args ...any) string {
	s, ok := catalogs[current][key]
	if !ok {
		s, ok = catalogs[English][key]
	}
	if !ok {
		s = key
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// TN is T for a count n: a catalog may hold a ".one" form of the key, used
// when n is 1 (English has one; Korean has no plural and does not need it).
func TN(key string, n int, args ...any) string {
	if n == 1 {
		if _, ok := catalogs[current][key+".one"]; ok {
			return T(key+".one", args...)
		}
	}
	return T(key, args...)
}

// catalogs maps language -> key -> text. Texts are short phrases: a status
// says what is, a hint says what to press.
var catalogs = map[Lang]map[string]string{
	English: en,
	Korean:  ko,
}
