func connectWithRetry(dsn string) (*sql.DB, error) {
    var db *sql.DB
    var err error

    for i := 0; i < 10; i++ {
        db, err = sql.Open("mysql", dsn)
        if err == nil {
            err = db.Ping() 
            if err == nil {
                return db, nil
            }
        }
        log.Printf("DB not ready, retrying... (%d/10)", i+1)
        time.Sleep(1 * time.Second)
    }
    return nil, fmt.Errorf("could not connect to DB: %v", err)
}