package helpers

import (
	"database/sql"

	"kubendt/kubeclient"
)

var DB *sql.DB

func SetDB(db *sql.DB) {
	DB = db
}

func GetDB() *sql.DB {
	return DB
}

func DeletePositionsByNamespace(namespace string) error {
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}
	stmt := `DELETE FROM node_positions WHERE cluster_id = ? AND namespace = ?`
	_, err = DB.Exec(stmt, clusterID, namespace)
	return err
}
