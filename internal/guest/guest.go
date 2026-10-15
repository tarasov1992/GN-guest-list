package guest

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

type Priority string
type Source string

const (
	PriorityRegular Priority = "regular"
	PriorityVIP     Priority = "vip"
	SourceFile      Source   = "file"
	SourceWalkin    Source   = "walkin"
)

type Guest struct {
	ID         int64
	Source     Source
	Surname    string
	FirstName  string
	Patronymic string
	CheckedIn  bool
	Priority   Priority
	PlusOne    bool
	SpcTag     bool
	Removed    bool
}

func (g Guest) FullNameKey() string {
	return NormalizeName(g.Surname + " " + g.FirstName + " " + g.Patronymic)
}

func NormalizeName(name string) string {
	s := norm.NFC.String(name)
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		switch r {
		case 'ё':
			return 'е'
		case '\u2010', '\u2011', '\u2013', '\u2014', '\u2212':
			return '-'
		case '\u200b', '\u200c', '\u200d', '\ufeff', '\u00ad':
			return -1
		case 'a':
			return 'а'
		case 'b':
			return 'в'
		case 'c':
			return 'с'
		case 'e':
			return 'е'
		case 'h':
			return 'н'
		case 'k':
			return 'к'
		case 'm':
			return 'м'
		case 'o':
			return 'о'
		case 'p':
			return 'р'
		case 't':
			return 'т'
		case 'x':
			return 'х'
		case 'y':
			return 'у'
		default:
			return r
		}
	}, s)
	parts := strings.Fields(s)
	s = strings.Join(parts, " ")
	s = strings.ReplaceAll(s, " -", "-")
	s = strings.ReplaceAll(s, "- ", "-")
	return s
}
