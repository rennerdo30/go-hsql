package hsql

import "errors"

// execResult implements driver.Result.
type execResult struct {
	rowsAffected int64
	lastInsertID int64
	hasLastID    bool
}

// LastInsertId reports the last generated identity value, when available.
// HSQLDB only returns generated keys when explicitly requested; until that path
// is wired, this reports an error rather than a misleading zero.
func (r *execResult) LastInsertId() (int64, error) {
	if r.hasLastID {
		return r.lastInsertID, nil
	}
	return 0, errors.New("hsql: LastInsertId is not supported; query IDENTITY() or use a RETURNING clause")
}

// RowsAffected returns the number of rows affected by the statement.
func (r *execResult) RowsAffected() (int64, error) {
	return r.rowsAffected, nil
}
