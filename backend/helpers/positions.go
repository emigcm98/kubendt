package helpers

import "database/sql"

var DB *sql.DB

func SetDB(db *sql.DB) {
	DB = db
}

func GetDB() *sql.DB {
	return DB
}

// DeletePositionsByNamespace is the "delete positions" of a clear or a
// namespace delete: the layout's positions go, with their mirror.
func DeletePositionsByNamespace(namespace string) error {
	return ClearPositions(namespace)
}
