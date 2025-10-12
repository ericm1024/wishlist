package main

import (
    "context"
	"database/sql"
	"encoding/json"	
    "fmt"
    "io"
    "log"
    "net"					
    "net/http"
    "os"			
    "os/signal"
    "sync"
    "sync/atomic"	
    "time"				

	_ "github.com/mattn/go-sqlite3" // Import the SQLite driver
	"github.com/urfave/negroni"
)

type Config struct {
	DbPath string
	Host string
	Port string
}

// ServeHTTP implements the http.Handler interface
func handeUsers(logger *log.Logger, db *sql.DB) http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {

		if r.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		
		// Query data
		rows, err := db.Query("SELECT id, name FROM users")
		if err != nil {
			logger.Fatalf("Error querying data: %v", err)
		}
		defer rows.Close()
		
		fmt.Fprintf(w, "Users in the database:")
		for rows.Next() {
			var id int
			var name string
			err = rows.Scan(&id, &name)
			if err != nil {
				logger.Fatalf("Error scanning row: %v", err)
			}
			fmt.Fprintf(w, "ID: %d, Name: %s\n", id, name)
		}
		err = rows.Err()
		if err != nil {
			logger.Fatalf("Error iterating rows: %v", err)
		}
	}
}

func handleLogin(logger *log.Logger, db *sql.DB) http.HandlerFunc {

	// The struct that represents the expected JSON body.
	type LoginRequest struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	type LoginResponse struct {
		Answer string `json:"answer"`
	}
	
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Enforce the request method (e.g., POST).
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		// 2. Enforce the Content-Type header.
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "Unsupported Media Type", http.StatusUnsupportedMediaType)
			return
		}

		// 3. Decode the request body into a Go struct.
		var reqBody LoginRequest
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&reqBody); err != nil {
			http.Error(w, "Bad Request: Malformed JSON", http.StatusBadRequest)
			return
		}

		// Make sure the request body stream is closed.
		defer r.Body.Close()
	

		// Set the Content-Type header to indicate JSON
		w.Header().Set("Content-Type", "application/json")
		
		// Create a new JSON encoder that writes directly to the http.ResponseWriter
		encoder := json.NewEncoder(w)

		// Encode the data and write it to the response
		response := LoginResponse{Answer: "login poggers"}
		if err := encoder.Encode(response); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}		
	}
}

func initDb(logger *log.Logger, config *Config) *sql.DB {
	// Open (or create) the SQLite database file
	db, err := sql.Open("sqlite3", config.DbPath)
	if err != nil {
		logger.Fatalf("Error opening database: %v", err)
	}
	// TODO: when to do this
	//defer db.Close() // Ensure the database connection is closed when main exits

	// Create users table
	sqlStmt := `
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL
	);
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		logger.Fatalf("Error creating table: %v", err)
	}
	logger.Println("Table 'users' created or already exists.")	

	// Insert data
	/*
	stmt, err := db.Prepare("INSERT INTO users(name) VALUES(?)")
	if err != nil {
		logger.Fatalf("Error preparing insert statement: %v", err)
	}
	defer stmt.Close()

	_, err = stmt.Exec("Eric")
	if err != nil {
		logger.Fatalf("Error inserting data for Eric: %v", err)
	}
	logger.Println("Inserted Eric.")
*/
	return db
}

func addRoutes(
	mux                 *http.ServeMux,
	logger              *log.Logger,
	config              *Config,
	db                  *sql.DB,
) {
	mux.Handle("/api/users", handeUsers(logger, db))
	mux.Handle("/api/login", handleLogin(logger, db))
}

var requestIdCounter atomic.Uint64

func loggingMiddleware(logger *log.Logger, handler http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := requestIdCounter.Add(1)
		
		logger.Printf("%d %s %s %s START", id, r.RemoteAddr, r.Method, r.URL.Path)
		lrw := negroni.NewResponseWriter(w)
		handler.ServeHTTP(lrw, r)

		statusCode := lrw.Status()
		logger.Printf("%d %s %s %s FINISH %d %s", id, r.RemoteAddr, r.Method, r.URL.Path,
			statusCode, http.StatusText(statusCode))
	}
}

func NewServer(
	logger *log.Logger,
	config *Config,
	db * sql.DB,
) http.Handler {
	mux := http.NewServeMux()
	addRoutes(
		mux,
		logger,
		config,
		db,
	)
	handler := loggingMiddleware(logger, mux)
	return handler
}

func run(ctx context.Context, w io.Writer, args []string) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	logger := log.Default()

	config := &Config{
		DbPath: "./example.db",
		Host: "localhost",
		Port: "8080",
	}

	db := initDb(logger, config)
	
	srv := NewServer(logger, config, db)
	httpServer := &http.Server{
		Addr:    net.JoinHostPort(config.Host, config.Port),
		Handler: srv,
	}
	go func() {
		logger.Printf("listening on %s\n", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "error listening and serving: %s\n", err)
		}
	}()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		shutdownCtx := context.Background()
		shutdownCtx, cancel := context.WithTimeout(shutdownCtx, 10 * time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "error shutting down http server: %s\n", err)
		}
	}()
	wg.Wait()
	return nil
}

func main() {
	ctx := context.Background()
	if err := run(ctx, os.Stdout, os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}
