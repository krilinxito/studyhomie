package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/krilinxito/studyhomie/internal/i18n"
)

// lang is the interface language the session talks in.
func (s *Session) lang() string { return i18n.UI(s.Cfg.Language) }

// aiLang is the language the prompts ask the model to write in.
func (s *Session) aiLang() string { return i18n.AI(s.Cfg.Language) }

// tr translates a message written in Spanish into the session's language.
func (s *Session) tr(msg string) string { return i18n.T(s.lang(), msg) }

// trf translates a format and fills it in.
func (s *Session) trf(format string, a ...any) string { return i18n.Sprintf(s.lang(), format, a...) }

// h is the heading of a note section (by its Spanish name) in the session's language. Sections
// are found in any language, so this only decides how a new or rewritten one reads.
func (s *Session) h(name string) string { return i18n.Heading(s.lang(), name) }

// yesNo ends the label of a yes/no question.
const yesNo = " (s/n)"

var _ = i18n.N("(s/n)") // translated in Label

// Label translates the label of a question into language c. A yes/no question ends in " (s/n)",
// which is translated apart.
func Label(c, label string) string {
	if q, ok := strings.CutSuffix(label, yesNo); ok {
		return i18n.T(c, q) + " " + i18n.T(c, strings.TrimSpace(yesNo))
	}
	return i18n.T(c, label)
}

// yes reports whether a typed answer means yes, in any of the interface languages.
func yes(a string) bool {
	switch strings.ToLower(strings.TrimSpace(a)) {
	case "s", "si", "sí", "y", "yes", "o", "oui", "是", "sim", "ok":
		return true
	}
	return false
}

// localError is an error whose message is translated when it is shown. Its format is written in
// Spanish, like every message, and it is filled in for the language of whoever reads it.
type localError struct {
	format string
	args   []any
	input  bool // a mistake of the request, not a failure: no guide yet, an empty question
}

func (e *localError) Error() string { return fmt.Errorf(e.format, e.args...).Error() }

func (e *localError) Unwrap() error {
	for _, a := range e.args {
		if err, ok := a.(error); ok {
			return err
		}
	}
	return nil
}

// text is the message in language c; wrapped errors of this kind are translated too.
func (e *localError) text(c string) string {
	args := make([]any, len(e.args))
	for i, a := range e.args {
		if err, ok := a.(error); ok {
			a = ErrorText(err, c)
		}
		args[i] = a
	}
	return fmt.Sprintf(strings.ReplaceAll(i18n.T(c, e.format), "%w", "%v"), args...)
}

// inputErrorf makes an error the learner can fix by changing what they asked.
func inputErrorf(format string, a ...any) error {
	return &localError{format: format, args: a, input: true}
}

// failf makes an error worth showing to the learner, translated when it is shown.
func failf(format string, a ...any) error { return &localError{format: format, args: a} }

// IsInputError reports whether err is something the learner can fix by changing what they asked.
func IsInputError(err error) bool {
	var e *localError
	return errors.As(err, &e) && e.input
}

// ErrorText is err's message in interface language c (a code from i18n.UI). Errors from outside
// StudyHomie (a provider, the file system) stay as they came.
func ErrorText(err error, c string) string {
	if err == nil {
		return ""
	}
	var e *localError
	if errors.As(err, &e) && e == err {
		return e.text(c)
	}
	if errors.As(err, &e) {
		// a plain wrapper around ours: translate ours, keep the wrapper's text around it
		return strings.Replace(err.Error(), e.Error(), e.text(c), 1)
	}
	return err.Error()
}

// errText is err's message in the session's language.
func (s *Session) errText(err error) string { return ErrorText(err, s.lang()) }
