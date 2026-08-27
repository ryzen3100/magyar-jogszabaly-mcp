package builddb

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/seed"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// isoMillis mirrors JavaScript's new Date().toISOString() (millisecond
// precision, trailing Z for UTC).
const isoMillis = "2006-01-02T15:04:05.000Z07:00"

const (
	insertDocSQL = `
    INSERT INTO legal_documents
      (id, type, title, title_en, short_name, status, issued_date, in_force_date, url, description)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	insertProvisionSQL = `
    INSERT INTO legal_provisions (document_id, provision_ref, chapter, section, title, content, metadata)
    VALUES (?, ?, ?, ?, ?, ?, ?)`

	insertDefinitionSQL = `
    INSERT OR IGNORE INTO definitions (document_id, term, term_en, definition, source_provision)
    VALUES (?, ?, ?, ?, ?)`

	euDocumentInsertSQL = `
    INSERT OR IGNORE INTO eu_documents (id, type, year, number, community, title, short_name, url_eur_lex, description)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	euReferenceInsertSQL = `
    INSERT INTO eu_references
      (source_type, source_id, document_id, provision_id, eu_document_id, eu_article,
       reference_type, reference_context, full_citation, is_primary_implementation,
       implementation_status, last_verified)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
)

// euMapping is one row of data/eu-mappings.json.
type euMapping struct {
	HungarianDocumentID  string `json:"hungarian_document_id"`
	EUDocumentID         string `json:"eu_document_id"`
	EUType               string `json:"eu_type"`
	EUYear               int    `json:"eu_year"`
	EUNumber             int    `json:"eu_number"`
	EUCommunity          string `json:"eu_community"`
	EUTitle              string `json:"eu_title"`
	EUShortName          string `json:"eu_short_name"`
	ReferenceType        string `json:"reference_type"`
	IsPrimary            bool   `json:"is_primary"`
	ImplementationStatus string `json:"implementation_status"`
}

// Build creates the SQLite database at outPath from the seed JSON files in
// seedDir, plus the manual EU mappings — port of buildDatabase() in
// scripts/build-db.ts:304-546. logf receives progress output (the command
// sends it to stderr; the TS original printed to stdout).
func Build(outPath, seedDir, euMappingsPath string, logf func(format string, args ...any)) error {
	logf("Building Hungarian Law MCP database...\n")

	// Build into a sibling temp file and publish with a single rename once the
	// database is fully settled — a failed build must never destroy the
	// last-good artifact (the TS original deleted outPath up front).
	tmpPath := outPath + ".tmp"
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	for _, p := range []string{tmpPath, tmpPath + "-wal", tmpPath + "-shm"} {
		// A leftover from an interrupted run would poison this build.
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clear %s: %w", p, err)
		}
	}
	defer os.Remove(tmpPath) // best-effort cleanup on failure; no-op after a successful rename

	db, err := sql.Open("sqlite", "file:"+tmpPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	// One connection: the per-connection PRAGMAs then apply to every statement,
	// and a single-writer batch build gains nothing from a pool.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("foreign_keys pragma: %w", err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		return fmt.Errorf("journal_mode pragma: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}

	seedFiles, err := listSeedFiles(seedDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logf("No seed directory at %s — creating empty database.", seedDir)
			return publish(db, tmpPath, outPath)
		}
		return err
	}
	if len(seedFiles) == 0 {
		logf("No seed files found. Database created with empty schema.")
		return publish(db, tmpPath, outPath)
	}

	var totalDocs, totalProvisions, totalDefs, totalEUDocuments, totalEUReferences, euRefFailures int
	primarySeen := map[string]bool{} // "docID:euDocumentID" -> already has its first 'implements' ref

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin seed transaction: %w", err)
	}
	defer tx.Rollback() // no-op once committed

	insertDoc, err := tx.Prepare(insertDocSQL)
	if err != nil {
		return fmt.Errorf("prepare insertDoc: %w", err)
	}
	insertProvision, err := tx.Prepare(insertProvisionSQL)
	if err != nil {
		return fmt.Errorf("prepare insertProvision: %w", err)
	}
	insertDefinition, err := tx.Prepare(insertDefinitionSQL)
	if err != nil {
		return fmt.Errorf("prepare insertDefinition: %w", err)
	}
	insertEUDocument, err := tx.Prepare(euDocumentInsertSQL)
	if err != nil {
		return fmt.Errorf("prepare insertEUDocument: %w", err)
	}
	insertEUReference, err := tx.Prepare(euReferenceInsertSQL)
	if err != nil {
		return fmt.Errorf("prepare insertEUReference: %w", err)
	}

	for i, name := range seedFiles {
		// A full corpus build is silent for minutes at a time; heartbeat so
		// the log does not look stalled.
		if (i+1)%500 == 0 {
			logf("  Processed %d/%d seed files", i+1, len(seedFiles))
		}
		data, err := os.ReadFile(filepath.Join(seedDir, name))
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		var s seed.DocumentSeed
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("parse %s: %w", name, err)
		}
		if s.Type == "" {
			s.Type = "statute"
		}
		if s.Status == "" {
			s.Status = "in_force"
		}

		if _, err := insertDoc.Exec(s.ID, s.Type, s.Title, nullIfEmpty(s.TitleEn), nullIfEmpty(s.ShortName), s.Status,
			nullIfEmpty(s.IssuedDate), nullIfEmpty(s.InForceDate), nullIfEmpty(s.URL), nullIfEmpty(s.Description)); err != nil {
			return fmt.Errorf("insert document %s: %w", s.ID, err)
		}
		totalDocs++

		for _, p := range dedupeProvisions(s.Provisions) {
			var metadata any
			if p.Metadata != nil {
				b, err := json.Marshal(p.Metadata)
				if err != nil {
					return fmt.Errorf("marshal metadata for %s:%s: %w", s.ID, p.ProvisionRef, err)
				}
				metadata = string(b)
			}
			res, err := insertProvision.Exec(s.ID, p.ProvisionRef, nullIfEmpty(p.Chapter), p.Section,
				nullIfEmpty(p.Title), p.Content, metadata)
			if err != nil {
				return fmt.Errorf("insert provision %s:%s: %w", s.ID, p.ProvisionRef, err)
			}
			totalProvisions++
			provisionID, err := res.LastInsertId()
			if err != nil {
				return fmt.Errorf("last_insert_rowid for %s:%s: %w", s.ID, p.ProvisionRef, err)
			}

			refs := ExtractEUReferences(p.Content)
			if len(refs) == 0 {
				continue
			}
			sourceID := s.ID + ":" + p.ProvisionRef
			lastVerified := time.Now().UTC().Format(isoMillis)

			for _, ref := range refs {
				capType := "Directive"
				if ref.Type == "regulation" {
					capType = "Regulation"
				}
				shortName := fmt.Sprintf("%s %d/%d", capType, ref.Year, ref.Number)
				eurLexType := "dir"
				if ref.Type == "regulation" {
					eurLexType = "reg"
				}
				eurLexURL := fmt.Sprintf("https://eur-lex.europa.eu/eli/%s/%d/%d/oj", eurLexType, ref.Year, ref.Number)

				euRes, err := insertEUDocument.Exec(ref.EUDocumentID, ref.Type, ref.Year, ref.Number, ref.Community,
					shortName, shortName, eurLexURL, "Auto-extracted from Hungarian statute text")
				if err != nil {
					return fmt.Errorf("insert eu_document %s: %w", ref.EUDocumentID, err)
				}
				if n, err := euRes.RowsAffected(); err == nil && n > 0 {
					totalEUDocuments++
				}

				primaryKey := s.ID + ":" + ref.EUDocumentID
				isPrimary := 0
				if ref.ReferenceType == "implements" && !primarySeen[primaryKey] {
					isPrimary = 1
					primarySeen[primaryKey] = true
				}
				implStatus := "unknown"
				if isPrimary == 1 {
					implStatus = "complete"
				}
				var article any // NULL where the TS original passed null
				if ref.EUArticle != "" {
					article = ref.EUArticle
				}
				// The TS original swallowed every error here (bare catch{}),
				// silently dropping rows from the shipped database. UNIQUE
				// duplicates are still tolerated, but every other failure is
				// now counted and reported in the summary below.
				if _, err := insertEUReference.Exec("provision", sourceID, s.ID, provisionID, ref.EUDocumentID, article,
					ref.ReferenceType, ref.ReferenceContext, ref.FullCitation, isPrimary, implStatus, lastVerified); err != nil {
					if !isUniqueViolation(err) {
						euRefFailures++
					}
				} else {
					totalEUReferences++
				}
			}
		}

		for _, d := range s.Definitions {
			if _, err := insertDefinition.Exec(s.ID, d.Term, nil, d.Definition, nullIfEmpty(d.SourceProvision)); err != nil {
				return fmt.Errorf("insert definition %s:%s: %w", s.ID, d.Term, err)
			}
			totalDefs++
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit seed transaction: %w", err)
	}

	euDocs, euRefs, err := applyEUMappings(db, euMappingsPath, logf)
	if err != nil {
		return err
	}
	totalEUDocuments += euDocs
	totalEUReferences += euRefs

	if err := writeBuildMetadata(db, time.Now().UTC().Format(isoMillis)); err != nil {
		return err
	}

	if err := publish(db, tmpPath, outPath); err != nil {
		return err
	}

	info, err := os.Stat(outPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", outPath, err)
	}
	if euRefFailures > 0 {
		logf("  ⚠ %d EU references failed to insert", euRefFailures)
	}
	logf("\nBuild complete: %d documents, %d provisions, %d definitions, %d EU documents, %d EU references",
		totalDocs, totalProvisions, totalDefs, totalEUDocuments, totalEUReferences)
	logf("Output: %s (%.1f MB)", outPath, float64(info.Size())/1024/1024)
	return nil
}

// applyEUMappings loads the manual EU mappings file and inserts its
// document-level EU documents/references, skipping mappings whose Hungarian
// document is absent from the database. A missing file is not an error: the
// build continues without manual mappings (with a warning, as in TS).
// Returns the number of EU documents and references inserted.
func applyEUMappings(db *sql.DB, euMappingsPath string, logf func(format string, args ...any)) (int, int, error) {
	raw, err := os.ReadFile(euMappingsPath)
	if errors.Is(err, os.ErrNotExist) {
		logf("  ⚠ No EU mappings file at %s — building without manual mappings.", euMappingsPath)
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("read %s: %w", euMappingsPath, err)
	}
	var mappings []euMapping
	if err := json.Unmarshal(raw, &mappings); err != nil {
		return 0, 0, fmt.Errorf("parse %s: %w", euMappingsPath, err)
	}

	totalEUDocuments, totalEUReferences := 0, 0
	mtx, err := db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("begin EU mappings transaction: %w", err)
	}
	defer mtx.Rollback() // no-op once committed
	// The TS original re-prepared these per row to work around stale
	// statement state in @ansvar/mcp-sqlite; modernc has no such issue.
	mInsertEUDocument, err := mtx.Prepare(euDocumentInsertSQL)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare mapping insertEUDocument: %w", err)
	}
	mInsertEUReference, err := mtx.Prepare(euReferenceInsertSQL)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare mapping insertEUReference: %w", err)
	}
	docExists, err := mtx.Prepare(`SELECT id FROM legal_documents WHERE id = ?`)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare docExists: %w", err)
	}

	for _, m := range mappings {
		res, err := mInsertEUDocument.Exec(m.EUDocumentID, m.EUType, m.EUYear, m.EUNumber, m.EUCommunity,
			m.EUTitle, m.EUShortName, nil, nil)
		if err != nil {
			return 0, 0, fmt.Errorf("insert eu_document %s: %w", m.EUDocumentID, err)
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			totalEUDocuments++
		}

		var id string
		err = docExists.QueryRow(m.HungarianDocumentID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			logf(`  ⚠ EU mapping skipped: Hungarian document "%s" not found in database`, m.HungarianDocumentID)
			continue
		} else if err != nil {
			return 0, 0, fmt.Errorf("check document %s: %w", m.HungarianDocumentID, err)
		}

		primary := 0
		if m.IsPrimary {
			primary = 1
		}
		if _, err := mInsertEUReference.Exec("document", m.HungarianDocumentID, m.HungarianDocumentID, nil,
			m.EUDocumentID, nil, m.ReferenceType, "Manual mapping: "+m.EUShortName, m.EUTitle, primary,
			m.ImplementationStatus, time.Now().UTC().Format(isoMillis)); err != nil {
			// Tolerate only UNIQUE (duplicate) failures, like the TS original.
			if !isUniqueViolation(err) {
				return 0, 0, fmt.Errorf("EU mapping insert failed for %s -> %s: %w", m.HungarianDocumentID, m.EUDocumentID, err)
			}
		} else {
			totalEUReferences++
		}
	}
	if err := mtx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit EU mappings transaction: %w", err)
	}
	logf("  Loaded %d EU mappings from seed file.", len(mappings))
	return totalEUDocuments, totalEUReferences, nil
}

// writeBuildMetadata records the db_metadata rows describing the build.
func writeBuildMetadata(db *sql.DB, builtAt string) error {
	mtx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin metadata transaction: %w", err)
	}
	defer mtx.Rollback() // no-op once committed
	insertMeta, err := mtx.Prepare(`INSERT INTO db_metadata (key, value) VALUES (?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insertMeta: %w", err)
	}
	for _, kv := range [][2]string{
		{"tier", "free"},
		{"schema_version", "2"},
		{"built_at", builtAt},
		{"builder", "build-db.go"},
		{"jurisdiction", "HU"},
		{"source", "official-source"},
		{"licence", "See sources.yml"},
	} {
		if _, err := insertMeta.Exec(kv[0], kv[1]); err != nil {
			return fmt.Errorf("insert metadata %s: %w", kv[0], err)
		}
	}
	if err := mtx.Commit(); err != nil {
		return fmt.Errorf("commit metadata transaction: %w", err)
	}
	return nil
}

// publish finalizes the temp database in place — back to journal_mode DELETE
// for WASM compatibility, then ANALYZE + VACUUM, as in TS — and closes it
// with a checked error, since the file must be fully written before it can be
// published. Only after a clean close with no -wal/-shm sidecars left beside
// it is the temp file renamed over outPath.
func publish(db *sql.DB, tmpPath, outPath string) error {
	if _, err := db.Exec(`PRAGMA journal_mode = DELETE`); err != nil {
		return fmt.Errorf("journal_mode pragma: %w", err)
	}
	if _, err := db.Exec(`ANALYZE`); err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	if _, err := db.Exec(`VACUUM`); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	for _, p := range []string{tmpPath + "-wal", tmpPath + "-shm"} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s still exists after close; refusing to publish an unsettled database", p)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", p, err)
		}
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		return fmt.Errorf("publish %s: %w", outPath, err)
	}
	return nil
}

// listSeedFiles returns the *.json file names in dir (not paths), excluding
// names starting with '.' or '_'. os.ReadDir returns them sorted by name.
// ponytail: sorted order is an intentional deviation from the TS original's
// unsorted readdirSync — rowids can differ between builds, content is
// identical; tools/parity/compare_db.py verifies the logical equivalence.
func listSeedFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".json") && !strings.HasPrefix(n, ".") && !strings.HasPrefix(n, "_") {
			names = append(names, n)
		}
	}
	return names, nil
}

// dedupeProvisions collapses duplicate provision_refs (after trimming),
// keeping the row whose whitespace-normalized content is longest and
// preserving first-occurrence order — port of dedupeProvisions in
// build-db.ts:238-248 (ties keep the existing row, as in TS).
func dedupeProvisions(provisions []seed.ProvisionSeed) []seed.ProvisionSeed {
	if len(provisions) == 0 {
		return nil
	}
	byRef := make(map[string]int, len(provisions))
	out := make([]seed.ProvisionSeed, 0, len(provisions))
	for _, p := range provisions {
		p.ProvisionRef = strings.TrimSpace(p.ProvisionRef)
		if i, ok := byRef[p.ProvisionRef]; ok {
			if len(collapseSpace(p.Content)) > len(collapseSpace(out[i].Content)) {
				out[i] = p
			}
			continue
		}
		byRef[p.ProvisionRef] = len(out)
		out = append(out, p)
	}
	return out
}

// nullIfEmpty maps Go's zero string to SQL NULL where the TS original used
// `x ?? null`. The corpus contains no explicitly-empty optional fields.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// isUniqueViolation reports an insert-tolerable duplicate. The modernc driver
// exposes *sqlite.Error with a result code, but extended result codes are off
// by default, so a UNIQUE violation is indistinguishable by code from any
// other SQLITE_CONSTRAINT — match on the message (see the driver's
// conn.ExtendedResultCodes if this ever needs to be exact).
func isUniqueViolation(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
