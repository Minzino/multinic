package controllers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
)

// DatabaseOptimizer provides database optimization features
type DatabaseOptimizer struct {
	db         *sql.DB
	stmtCache  sync.Map // map[string]*sql.Stmt
	bulkWriter *BulkWriter
	indexCache sync.Map // map[string]bool
}

// BulkWriter handles bulk database operations
type BulkWriter struct {
	insertBuffer  []BulkInsertItem
	updateBuffer  []BulkUpdateItem
	deleteBuffer  []BulkDeleteItem
	bufferSize    int
	flushInterval time.Duration
	mutex         sync.Mutex
	db            *sql.DB
	stopChan      chan struct{}
}

type BulkInsertItem struct {
	Table string
	Data  map[string]interface{}
}

type BulkUpdateItem struct {
	Table     string
	Data      map[string]interface{}
	Condition string
	Args      []interface{}
}

type BulkDeleteItem struct {
	Table     string
	Condition string
	Args      []interface{}
}

// NewDatabaseOptimizer creates a new database optimizer
func NewDatabaseOptimizer(db *sql.DB) *DatabaseOptimizer {
	optimizer := &DatabaseOptimizer{
		db:         db,
		bulkWriter: NewBulkWriter(db, 100, 5*time.Second), // 100개 버퍼, 5초 간격
	}

	// 데이터베이스 최적화 설정
	optimizer.optimizeDatabase()

	return optimizer
}

// NewBulkWriter creates a new bulk writer
func NewBulkWriter(db *sql.DB, bufferSize int, flushInterval time.Duration) *BulkWriter {
	bw := &BulkWriter{
		db:            db,
		bufferSize:    bufferSize,
		flushInterval: flushInterval,
		stopChan:      make(chan struct{}),
	}

	// 자동 플러시 시작
	go bw.autoFlush()

	return bw
}

// optimizeDatabase applies database optimizations
func (do *DatabaseOptimizer) optimizeDatabase() {
	// 데이터베이스 설정 최적화
	optimizations := []string{
		"SET GLOBAL innodb_buffer_pool_size = 1073741824", // 1GB
		"SET GLOBAL innodb_log_file_size = 268435456",     // 256MB
		"SET GLOBAL innodb_flush_log_at_trx_commit = 2",   // 성능 향상
		"SET GLOBAL query_cache_size = 134217728",         // 128MB
		"SET GLOBAL query_cache_type = ON",
		"SET GLOBAL max_connections = 1000",
		"SET GLOBAL innodb_thread_concurrency = 0",
		"SET GLOBAL innodb_read_io_threads = 16",
		"SET GLOBAL innodb_write_io_threads = 16",
	}

	for _, opt := range optimizations {
		do.db.Exec(opt) // 에러 무시 (권한 문제 가능)
	}
}

// GetPreparedStatement returns a cached prepared statement
func (do *DatabaseOptimizer) GetPreparedStatement(query string) (*sql.Stmt, error) {
	if cached, ok := do.stmtCache.Load(query); ok {
		return cached.(*sql.Stmt), nil
	}

	stmt, err := do.db.Prepare(query)
	if err != nil {
		return nil, err
	}

	do.stmtCache.Store(query, stmt)
	return stmt, nil
}

// OptimizedInsert performs optimized insert operation
func (do *DatabaseOptimizer) OptimizedInsert(ctx context.Context, table string, data map[string]interface{}) error {
	// 인덱스 존재 확인 및 자동 생성
	if err := do.ensureIndexes(table, data); err != nil {
		return err
	}

	// Prepared statement 사용
	columns := make([]string, 0, len(data))
	placeholders := make([]string, 0, len(data))
	values := make([]interface{}, 0, len(data))

	for col, val := range data {
		columns = append(columns, col)
		placeholders = append(placeholders, "?")
		values = append(values, val)
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		table,
		strings.Join(columns, ", "),
		strings.Join(placeholders, ", "))

	stmt, err := do.GetPreparedStatement(query)
	if err != nil {
		return err
	}

	_, err = stmt.ExecContext(ctx, values...)
	return err
}

// BatchInsert performs batch insert operation
func (do *DatabaseOptimizer) BatchInsert(ctx context.Context, table string, dataList []map[string]interface{}) error {
	if len(dataList) == 0 {
		return nil
	}

	// 첫 번째 레코드에서 컬럼 정보 추출
	firstRecord := dataList[0]
	columns := make([]string, 0, len(firstRecord))
	for col := range firstRecord {
		columns = append(columns, col)
	}

	// 배치 크기 최적화 (1000개씩)
	batchSize := 1000
	for i := 0; i < len(dataList); i += batchSize {
		end := i + batchSize
		if end > len(dataList) {
			end = len(dataList)
		}

		batch := dataList[i:end]
		if err := do.executeBatchInsert(ctx, table, columns, batch); err != nil {
			return err
		}
	}

	return nil
}

// executeBatchInsert executes a batch insert
func (do *DatabaseOptimizer) executeBatchInsert(ctx context.Context, table string, columns []string, batch []map[string]interface{}) error {
	if len(batch) == 0 {
		return nil
	}

	// VALUES 절 구성
	valueGroups := make([]string, len(batch))
	args := make([]interface{}, 0, len(batch)*len(columns))

	for i, record := range batch {
		placeholders := make([]string, len(columns))
		for j, col := range columns {
			placeholders[j] = "?"
			args = append(args, record[col])
		}
		valueGroups[i] = "(" + strings.Join(placeholders, ", ") + ")"
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES %s",
		table,
		strings.Join(columns, ", "),
		strings.Join(valueGroups, ", "))

	_, err := do.db.ExecContext(ctx, query, args...)
	return err
}

// ensureIndexes ensures necessary indexes exist
func (do *DatabaseOptimizer) ensureIndexes(table string, data map[string]interface{}) error {
	indexKey := table + "_indexes_checked"
	if _, checked := do.indexCache.Load(indexKey); checked {
		return nil
	}

	// 테이블별 권장 인덱스
	recommendedIndexes := map[string][]string{
		"multi_interface": {
			"CREATE INDEX IF NOT EXISTS idx_interface_cr ON multi_interface(cr_namespace, cr_name)",
			"CREATE INDEX IF NOT EXISTS idx_interface_vm ON multi_interface(vm_name)",
			"CREATE INDEX IF NOT EXISTS idx_interface_mac ON multi_interface(mac_address)",
		},
		"multi_subnet": {
			"CREATE INDEX IF NOT EXISTS idx_subnet_id ON multi_subnet(subnet_id)",
			"CREATE INDEX IF NOT EXISTS idx_subnet_name ON multi_subnet(subnet_name)",
		},
		"node_table": {
			"CREATE INDEX IF NOT EXISTS idx_node_vm ON node_table(vm_name)",
			"CREATE INDEX IF NOT EXISTS idx_node_id ON node_table(vm_id)",
		},
		"cr_state": {
			"CREATE INDEX IF NOT EXISTS idx_cr_state ON cr_state(cr_namespace, cr_name)",
			"CREATE INDEX IF NOT EXISTS idx_cr_updated ON cr_state(last_updated)",
		},
	}

	if indexes, exists := recommendedIndexes[table]; exists {
		for _, indexSQL := range indexes {
			do.db.Exec(indexSQL) // 에러 무시 (이미 존재할 수 있음)
		}
	}

	do.indexCache.Store(indexKey, true)
	return nil
}

// AddBulkInsert adds an item to bulk insert buffer
func (bw *BulkWriter) AddBulkInsert(table string, data map[string]interface{}) {
	bw.mutex.Lock()
	defer bw.mutex.Unlock()

	bw.insertBuffer = append(bw.insertBuffer, BulkInsertItem{
		Table: table,
		Data:  data,
	})

	if len(bw.insertBuffer) >= bw.bufferSize {
		go bw.flushInserts()
	}
}

// AddBulkUpdate adds an item to bulk update buffer
func (bw *BulkWriter) AddBulkUpdate(table string, data map[string]interface{}, condition string, args []interface{}) {
	bw.mutex.Lock()
	defer bw.mutex.Unlock()

	bw.updateBuffer = append(bw.updateBuffer, BulkUpdateItem{
		Table:     table,
		Data:      data,
		Condition: condition,
		Args:      args,
	})

	if len(bw.updateBuffer) >= bw.bufferSize {
		go bw.flushUpdates()
	}
}

// flushInserts flushes insert buffer
func (bw *BulkWriter) flushInserts() {
	bw.mutex.Lock()
	if len(bw.insertBuffer) == 0 {
		bw.mutex.Unlock()
		return
	}

	buffer := make([]BulkInsertItem, len(bw.insertBuffer))
	copy(buffer, bw.insertBuffer)
	bw.insertBuffer = bw.insertBuffer[:0]
	bw.mutex.Unlock()

	// 테이블별로 그룹화
	tableGroups := make(map[string][]map[string]interface{})
	for _, item := range buffer {
		if _, exists := tableGroups[item.Table]; !exists {
			tableGroups[item.Table] = make([]map[string]interface{}, 0)
		}
		tableGroups[item.Table] = append(tableGroups[item.Table], item.Data)
	}

	// 각 테이블별로 배치 실행
	for table, dataList := range tableGroups {
		bw.executeBatchInsertForTable(table, dataList)
	}
}

// flushUpdates flushes update buffer
func (bw *BulkWriter) flushUpdates() {
	bw.mutex.Lock()
	if len(bw.updateBuffer) == 0 {
		bw.mutex.Unlock()
		return
	}

	buffer := make([]BulkUpdateItem, len(bw.updateBuffer))
	copy(buffer, bw.updateBuffer)
	bw.updateBuffer = bw.updateBuffer[:0]
	bw.mutex.Unlock()

	// 업데이트 실행
	for _, item := range buffer {
		bw.executeUpdate(item)
	}
}

// executeBatchInsertForTable executes batch insert for a specific table
func (bw *BulkWriter) executeBatchInsertForTable(table string, dataList []map[string]interface{}) error {
	if len(dataList) == 0 {
		return nil
	}

	// ON DUPLICATE KEY UPDATE를 사용한 UPSERT
	firstRecord := dataList[0]
	columns := make([]string, 0, len(firstRecord))
	for col := range firstRecord {
		columns = append(columns, col)
	}

	// VALUES 절 구성
	valueGroups := make([]string, len(dataList))
	args := make([]interface{}, 0, len(dataList)*len(columns))

	for i, record := range dataList {
		placeholders := make([]string, len(columns))
		for j, col := range columns {
			placeholders[j] = "?"
			args = append(args, record[col])
		}
		valueGroups[i] = "(" + strings.Join(placeholders, ", ") + ")"
	}

	// UPDATE 절 구성 (PRIMARY KEY가 아닌 컬럼들)
	updateParts := make([]string, 0, len(columns))
	for _, col := range columns {
		if col != "id" { // 일반적인 PRIMARY KEY 이름
			updateParts = append(updateParts, fmt.Sprintf("%s = VALUES(%s)", col, col))
		}
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES %s ON DUPLICATE KEY UPDATE %s",
		table,
		strings.Join(columns, ", "),
		strings.Join(valueGroups, ", "),
		strings.Join(updateParts, ", "))

	_, err := bw.db.Exec(query, args...)
	return err
}

// executeUpdate executes a single update
func (bw *BulkWriter) executeUpdate(item BulkUpdateItem) error {
	setParts := make([]string, 0, len(item.Data))
	args := make([]interface{}, 0, len(item.Data)+len(item.Args))

	for col, val := range item.Data {
		setParts = append(setParts, col+" = ?")
		args = append(args, val)
	}

	args = append(args, item.Args...)

	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s",
		item.Table,
		strings.Join(setParts, ", "),
		item.Condition)

	_, err := bw.db.Exec(query, args...)
	return err
}

// autoFlush automatically flushes buffers at intervals
func (bw *BulkWriter) autoFlush() {
	ticker := time.NewTicker(bw.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			bw.flushInserts()
			bw.flushUpdates()
		case <-bw.stopChan:
			// 마지막 플러시
			bw.flushInserts()
			bw.flushUpdates()
			return
		}
	}
}

// Stop stops the bulk writer
func (bw *BulkWriter) Stop() {
	close(bw.stopChan)
}

// DatabaseHealthMonitor monitors database health
type DatabaseHealthMonitor struct {
	db       *sql.DB
	metrics  *DatabaseMetrics
	stopChan chan struct{}
}

type DatabaseMetrics struct {
	ConnectionsActive int64
	ConnectionsMax    int64
	QueriesPerSecond  int64
	SlowQueries       int64
	mutex             sync.RWMutex
}

// NewDatabaseHealthMonitor creates a new health monitor
func NewDatabaseHealthMonitor(db *sql.DB) *DatabaseHealthMonitor {
	monitor := &DatabaseHealthMonitor{
		db:       db,
		metrics:  &DatabaseMetrics{},
		stopChan: make(chan struct{}),
	}

	go monitor.startMonitoring()
	return monitor
}

// startMonitoring starts the monitoring loop
func (dhm *DatabaseHealthMonitor) startMonitoring() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			dhm.collectMetrics()
		case <-dhm.stopChan:
			return
		}
	}
}

// collectMetrics collects database metrics
func (dhm *DatabaseHealthMonitor) collectMetrics() {
	dhm.metrics.mutex.Lock()
	defer dhm.metrics.mutex.Unlock()

	// 활성 연결 수
	var activeConnections int64
	dhm.db.QueryRow("SHOW STATUS LIKE 'Threads_connected'").Scan(&activeConnections)
	dhm.metrics.ConnectionsActive = activeConnections

	// 최대 연결 수
	var maxConnections int64
	dhm.db.QueryRow("SHOW VARIABLES LIKE 'max_connections'").Scan(&maxConnections)
	dhm.metrics.ConnectionsMax = maxConnections

	// 초당 쿼리 수
	var qps int64
	dhm.db.QueryRow("SHOW STATUS LIKE 'Queries'").Scan(&qps)
	dhm.metrics.QueriesPerSecond = qps
}

// GetMetrics returns current database metrics
func (dhm *DatabaseHealthMonitor) GetMetrics() (int64, int64, int64, int64) {
	dhm.metrics.mutex.RLock()
	defer dhm.metrics.mutex.RUnlock()

	return dhm.metrics.ConnectionsActive, dhm.metrics.ConnectionsMax,
		dhm.metrics.QueriesPerSecond, dhm.metrics.SlowQueries
}

// Stop stops the health monitor
func (dhm *DatabaseHealthMonitor) Stop() {
	close(dhm.stopChan)
}
