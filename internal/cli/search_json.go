package cli

// searchJSONLine is one matching body line. Line is the 1-based line number in
// the file, the same number `--context` prints and `--open` jumps to.
type searchJSONLine struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

// searchJSONNote is the shared note record plus what matched in it.
type searchJSONNote struct {
	noteJSON
	Count   int              `json:"count"`
	Matches []searchJSONLine `json:"matches"`
}

// runSearchJSON is `--json`: the notes the default paths mode would print, in
// the same order, as {path, abs_path, title, count, matches} records. No match
// is still exit 1, after printing `[]`, so a script reads valid JSON either way.
func runSearchJSON(r *searchRun) error {
	paths, err := matchingSearchPaths(r)
	if err != nil {
		return err
	}
	records := make([]searchJSONNote, 0, len(paths))
	for _, p := range paths {
		record, err := searchJSONRecord(r, p)
		if err != nil {
			return err
		}
		records = append(records, record)
	}
	if err := writeJSON(r.cmd.OutOrStdout(), records); err != nil {
		return err
	}
	if len(records) == 0 {
		return noMatch("search")
	}
	return nil
}

func searchJSONRecord(r *searchRun, path string) (searchJSONNote, error) {
	record := searchJSONNote{noteJSON: newNoteJSON(r.root, path), Matches: []searchJSONLine{}}
	fileLines, bodyStart, err := readBodyLines(path)
	if err != nil {
		return searchJSONNote{}, err
	}
	for _, idx := range matchingBodyLines(fileLines, bodyStart, r.matcher) {
		record.Matches = append(record.Matches, searchJSONLine{Line: idx + 1, Text: fileLines[idx]})
	}
	record.Count = len(record.Matches)
	return record, nil
}
