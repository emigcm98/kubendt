package database

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

const (
	defaultDBPath = "./kubendt.db"
	legacyDBPath  = "./positions.db"

	// schemaVersion is the current DB schema version, tracked in the file via
	// PRAGMA user_version. New databases are created at this version directly
	// by createTables; existing ones are upgraded step by step in
	// applyMigrations. There is intentionally no migration below version 1:
	// 1.0.0 ships the first released schema, so no older database can exist in
	// the wild. Bump this and add a case to applyMigrations for each change.
	schemaVersion = 1
)

func InitDB() {
	var err error
	dbPath := strings.TrimSpace(os.Getenv("KUBENDT_DB_PATH"))
	if dbPath == "" {
		dbPath = defaultDBPath
	}

	if err := ensureDBDirectory(dbPath); err != nil {
		log.Fatalf("❌ Error preparing database directory: %v", err)
	}

	if err := migrateLegacyDB(legacyDBPath, dbPath); err != nil {
		log.Fatalf("❌ Error migrating legacy database: %v", err)
	}

	DB, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("❌ Error opening database: %v", err)
	}

	createTables()
	applyMigrations()
}

// applyMigrations upgrades an existing database to schemaVersion, running each
// pending step in order and stamping the new version. A brand-new database is
// created at the latest schema by createTables and simply gets stamped here.
func applyMigrations() {
	var version int
	if err := DB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		log.Fatalf("❌ Error reading database schema version: %v", err)
	}

	if version == schemaVersion {
		return
	}
	if version > schemaVersion {
		log.Fatalf("❌ Database schema version %d is newer than this build supports (%d); upgrade KubeNDT", version, schemaVersion)
	}

	// Future migrations go here, e.g.:
	//   if version < 2 { migrateTo2() }
	// Each step brings the schema forward one version and is guarded by the
	// version check so it runs exactly once.

	// PRAGMA does not accept bound parameters, so the version is interpolated
	// (it is a trusted integer constant, not user input).
	if _, err := DB.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		log.Fatalf("❌ Error stamping database schema version: %v", err)
	}
	log.Printf("📐 Database schema at version %d", schemaVersion)
}

func createTables() {
	queries := []string{
		`
		CREATE TABLE IF NOT EXISTS node_positions (
			cluster_id TEXT NOT NULL DEFAULT '',
			namespace TEXT,
			node_id TEXT,
			x REAL,
			y REAL,
			PRIMARY KEY (cluster_id, namespace, node_id)
		);`,
		`
		CREATE TABLE IF NOT EXISTS namespace_state (
			cluster_id TEXT NOT NULL DEFAULT '',
			namespace TEXT NOT NULL,
			has_topology INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (cluster_id, namespace)
		);`,
		`
		CREATE TABLE IF NOT EXISTS namespace_operations (
			cluster_id TEXT NOT NULL DEFAULT '',
			namespace TEXT NOT NULL,
			operation_type TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (cluster_id, namespace)
		);`,
		`
		CREATE TABLE IF NOT EXISTS link_uid_registry (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			cluster_id TEXT NOT NULL DEFAULT '',
			uid INTEGER NOT NULL,
			namespace TEXT NOT NULL,
			node_name TEXT NOT NULL,
			interface_name TEXT NOT NULL,
			peer_node_name TEXT,
			peer_interface_name TEXT,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`
		CREATE INDEX IF NOT EXISTS idx_link_uid_registry_uid
		ON link_uid_registry (cluster_id, uid);`,
		`
		CREATE INDEX IF NOT EXISTS idx_link_uid_registry_namespace
		ON link_uid_registry (cluster_id, namespace);`,
		`
		CREATE TABLE IF NOT EXISTS driver_operation_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			cluster_id TEXT NOT NULL DEFAULT '',
			namespace TEXT NOT NULL,
			pod_name TEXT NOT NULL,
			driver_type TEXT NOT NULL,
			action_type TEXT NOT NULL,
			action_json TEXT NOT NULL,
			executed_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`
		CREATE INDEX IF NOT EXISTS idx_driver_operation_history_namespace_pod
		ON driver_operation_history (cluster_id, namespace, pod_name);`,
		`
		CREATE INDEX IF NOT EXISTS idx_driver_operation_history_executed_at
		ON driver_operation_history (executed_at);`,
		`
		CREATE TABLE IF NOT EXISTS namespace_file_meta (
			cluster_id TEXT NOT NULL DEFAULT '',
			namespace TEXT NOT NULL,
			path TEXT NOT NULL,
			sensitive INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (cluster_id, namespace, path)
		);`,
		`
		CREATE INDEX IF NOT EXISTS idx_namespace_file_meta_ns
		ON namespace_file_meta (cluster_id, namespace);`,
		`
		CREATE TABLE IF NOT EXISTS clusters (
			cluster_id TEXT PRIMARY KEY,
			context_name TEXT,
			server TEXT,
			last_seen TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`
		CREATE TABLE IF NOT EXISTS auth_config (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			password_hash TEXT NOT NULL
		);`,
		`
		CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			identity TEXT NOT NULL,
			roles TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			last_seen_at INTEGER NOT NULL
		);`,
		`
		CREATE TABLE IF NOT EXISTS api_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			token_hash TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			last_used_at INTEGER,
			expires_at INTEGER
		);`,
	}

	for _, query := range queries {
		if _, err := DB.Exec(query); err != nil {
			log.Fatalf("❌ Error creating table: %v", err)
		}
	}
}

func ensureDBDirectory(dbPath string) error {
	dir := filepath.Dir(dbPath)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}

func migrateLegacyDB(oldPath, newPath string) error {
	if oldPath == newPath {
		return nil
	}

	if _, err := os.Stat(newPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if _, err := os.Stat(oldPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	source, err := os.Open(oldPath)
	if err != nil {
		return err
	}
	defer source.Close()

	target, err := os.Create(newPath)
	if err != nil {
		return err
	}
	defer target.Close()

	if _, err := io.Copy(target, source); err != nil {
		return err
	}

	log.Printf("ℹ️ Migrated legacy database from %s to %s", oldPath, newPath)
	return nil
}
