package prisjeger

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	noteStartMarker = "<!-- prisjeger:start -->"
	noteEndMarker   = "<!-- prisjeger:end -->"
)

func findNoteBlock(content string) (start, end int, ok bool) {
	start = strings.Index(content, noteStartMarker)
	if start < 0 {
		return 0, 0, false
	}
	rel := strings.Index(content[start+len(noteStartMarker):], noteEndMarker)
	if rel < 0 {
		return start, len(content), true
	}
	return start, start + len(noteStartMarker) + rel + len(noteEndMarker), true
}

func writeNote(path string, rows map[string][]storedRow) error {
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	content := string(existing)
	block := noteBlock(rows, time.Now())

	if start, end, ok := findNoteBlock(content); ok {
		content = content[:start] + block + content[end:]
		return writeFileAtomic(path, []byte(content))
	}
	if content == "" {
		content = block + "\n"
	} else {
		content = strings.TrimRight(content, "\n") + "\n\n" + block + "\n"
	}
	return writeFileAtomic(path, []byte(content))
}

func noteBlock(rows map[string][]storedRow, now time.Time) string {
	var b strings.Builder
	b.WriteString(noteStartMarker)
	b.WriteString("\n")
	items := sortedItems(rows)
	if len(items) == 0 {
		b.WriteString("_No offers yet._\n")
		b.WriteString(noteEndMarker)
		return b.String()
	}
	fmt.Fprintf(&b, "_Updated %s. Source: results.csv._\n\n", now.Format("2006-01-02 15:04"))
	b.WriteString("| Item | Product | Price | Ship | Total | Ordinary | Sale | Low | Stock | Trustpilot | Rating | Status | Change | Deal | Region | Store | Link | Note |\n")
	b.WriteString("| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, item := range items {
		for _, row := range sortedByTotal(rows[item]) {
			link := ""
			if row.url != "" {
				link = "[open](<" + row.url + ">)"
			}
			cells := []string{
				row.item,
				row.product,
				compactNOK(row.price),
				compactNOK(row.shipping),
				compactNOK(row.total),
				compactNOK(row.ordinary),
				saleCell(row.salePct),
				compactNOK(row.historyLow),
				stockCell(row.stock, row.stockDetail),
				ratingCell(row.trustRating, row.trustCount),
				ratingCell(row.rating, row.ratingCount),
				row.status,
				changeCell(row),
				row.dealCheck,
				strings.ToUpper(row.storeRegion),
				row.store,
				link,
				row.note,
			}
			for i := range cells {
				cells[i] = escapeCell(cells[i])
			}
			b.WriteString("| ")
			b.WriteString(strings.Join(cells, " | "))
			b.WriteString(" |\n")
		}
	}
	b.WriteString(noteEndMarker)
	return b.String()
}

func compactNOK(v string) string {
	return strings.TrimSuffix(v, ".00")
}

func saleCell(v string) string {
	if v == "" {
		return ""
	}
	return v + "%"
}

func ratingCell(rating, count string) string {
	switch {
	case rating == "":
		return ""
	case count == "":
		return rating
	default:
		return rating + " (" + count + ")"
	}
}

// stockCell puts the page's quantity or low-stock wording next to the state,
// so "in_stock (2 igjen)" says both whether it is buyable and whether to hurry.
func stockCell(stock, detail string) string {
	if detail == "" {
		return stock
	}
	if stock == "" || stock == "unknown" {
		return detail
	}
	return stock + " (" + detail + ")"
}

func changeCell(row storedRow) string {
	switch row.status {
	case "change":
		return row.direction + " " + row.changeNOK + " (" + row.changePct + "%)"
	case "same":
		return "same"
	default:
		return ""
	}
}

func escapeCell(v string) string {
	return strings.ReplaceAll(v, "|", `\|`)
}

func writeFileAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".prisjeger-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
