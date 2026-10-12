package guestfile

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"checkin/internal/guest"

	"github.com/xuri/excelize/v2"
)

const (
	colSurname    = "Фамилия"
	colFirstName  = "Имя"
	colPatronymic = "Отчество"
	colPriority   = "Приоритет"
	// colPriorityAlt is the same column under the name the live file uses.
	colPriorityAlt = "Статус"
)

func Parse(r io.Reader) ([]guest.Guest, error) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return nil, fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("workbook has no sheets")
	}
	sheet := sheets[0]
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("read xlsx: %w", err)
	}
	if len(rows) < 9 {
		return nil, fmt.Errorf("header row not found")
	}

	cols := make(map[string]int)
	for i, col := range rows[8] {
		cols[strings.TrimSpace(col)] = i
	}
	if _, ok := cols[colPriority]; !ok {
		if i, ok := cols[colPriorityAlt]; ok {
			cols[colPriority] = i
		}
	}
	target := []string{
		colSurname,
		colFirstName,
		colPatronymic,
		colPriority,
	}
	for _, t := range target {
		if _, ok := cols[t]; !ok {
			return nil, fmt.Errorf("column %q is missing", t)
		}
	}

	nameKeyRows := make(map[string]int)
	guests := []guest.Guest{}
	errs := []error{}
	for i, row := range rows[9:] {
		excelRow := i + 10
		var g guest.Guest
		surname := cell(row, cols[colSurname])
		firstName := cell(row, cols[colFirstName])
		patronymic := cell(row, cols[colPatronymic])
		if surname == "" && firstName == "" && patronymic == "" {
			continue
		}
		if surname == "" {
			errs = append(errs, fmt.Errorf("row %d: missing surname", excelRow))
			continue
		}
		g.Surname = surname
		g.FirstName = firstName
		g.Patronymic = patronymic
		priority, err := parsePriority(cell(row, cols[colPriority]))
		if err != nil {
			errs = append(errs, fmt.Errorf("row %d: %w", excelRow, err))
			continue
		}
		g.Priority = priority

		guests = append(guests, g)
		fullNameKey := g.FullNameKey()
		if r, ok := nameKeyRows[fullNameKey]; !ok {
			nameKeyRows[fullNameKey] = excelRow
		} else {
			errs = append(errs, fmt.Errorf("row %d: duplicate of the row %d", excelRow, r))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return guests, nil
}

func cell(row []string, i int) string {
	if i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

func parsePriority(s string) (guest.Priority, error) {
	switch strings.ToLower(s) {
	case "vip":
		return guest.PriorityVIP, nil
	case "", "обычный":
		return guest.PriorityRegular, nil
	}
	return "", fmt.Errorf("unknown priority %q", s)
}
