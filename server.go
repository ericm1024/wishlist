// TODOs
// * use DisallowUnknownFields and ensure non-nil fields for all json requests
// * client & server side input validation for signup form
// * client & server side input validation for login form
// * normalize password strings? https://stackoverflow.com/a/66899076
// * limit field sizes for all client-controlled fields
// * remove expired sessions
// * resend session cookies periodically

package main

import (
    "context"
    "crypto/rand"	
	"database/sql"
	"encoding/base64"
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

	"github.com/matthewhartstonge/argon2"
	
	_ "github.com/mattn/go-sqlite3"
	"github.com/urfave/negroni"
)

type Config struct {
	DbPath string
	Host string
	Port string
}

const sessionCookieKey = "wishlist_session_id"

// ServeHTTP implements the http.Handler interface
func handeUsers(logger *log.Logger, db *sql.DB) http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {

		if r.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		// Validate session
		sessionCookie, err := r.Cookie(sessionCookieKey)
		if err != nil {
			if err == http.ErrNoCookie {
				http.Error(w, "missing session cookie", http.StatusBadRequest)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}

		binaryCookie, err := base64.URLEncoding.DecodeString(sessionCookie.Value)
		if err != nil {
			http.Error(w, "undecodable session cookie", http.StatusBadRequest)
			return
		}
		
		stmt, err := db.Prepare("SELECT expiry_time FROM sessions WHERE session_cookie = ?")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer stmt.Close()

		var expiryTime time.Time
		err = stmt.QueryRow(binaryCookie).Scan(&expiryTime)
		if err != nil {
			// XXX: differentiate no DB entry vs "something weird"
			http.Error(w, "no cookie in db", http.StatusUnauthorized)
			return
		}

		if expiryTime.Before(time.Now()) {
			http.Error(w, "expired cookie", http.StatusUnauthorized)
			return
		}
		
		// Query data
		rows, err := db.Query("SELECT id, first_name FROM users")
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

// https://developer.mozilla.org/en-US/docs/Web/HTTP/Guides/Cookies
func createSession(logger *log.Logger, db *sql.DB, userId int64, userAgent string, w http.ResponseWriter) (error) {
	stmt, err := db.Prepare("INSERT INTO sessions(session_cookie, id, expiry_time, user_agent) VALUES(?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	// Note that no error handling is necessary, as Read always succeeds.
	sessionCookie := make([]byte, 32)
	rand.Read(sessionCookie)	

	maxAgeSeconds := 7 * 24 * 60 * 60
	
	// 7 day session liveness
	expiryTime := time.Now().Add(time.Duration(maxAgeSeconds) * time.Second)
	
	_, err = stmt.Exec(sessionCookie, userId, expiryTime, userAgent)
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
			Name: sessionCookieKey,
			Value: base64.URLEncoding.EncodeToString(sessionCookie),
			Expires: expiryTime,
			Secure: true,
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
	})

	logger.Printf("Created session for user id %d agent '%s' expires at %v", userId, userAgent,
		expiryTime)
	
	return nil
}

func handleLogin(logger *log.Logger, db *sql.DB) http.HandlerFunc {

	// The struct that represents the expected JSON body.
	type LoginRequest struct {
		Email string `json:"email"`
		Password string `json:"password"`
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
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&reqBody); err != nil {
			http.Error(w, "Bad Request: Malformed JSON", http.StatusBadRequest)
			return
		}

		if reqBody.Email == "" || reqBody.Password == "" {
			http.Error(w, "Bad Request: Missing fields", http.StatusBadRequest)
			return
		}

		// Make sure the request body stream is closed.
		defer r.Body.Close()

		stmt, err := db.Prepare("SELECT password_hash, id FROM users WHERE email = ?")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer stmt.Close()
		
		var passwordHash string
		var userID int64
		err = stmt.QueryRow(reqBody.Email).Scan(&passwordHash, &userID)
		if err != nil {
			// differentiate between DB issue and unknown error?
			http.Error(w, "invalid username or password", http.StatusUnauthorized)
			return
		}

		// TODO: hash either way to prevent timing attacks to find valid emails?
		
		ok, err := argon2.VerifyEncoded([]byte(reqBody.Password), []byte(passwordHash))
		if err != nil || !ok {
			http.Error(w, "invalid username or password", http.StatusUnauthorized)
			return
		}		

		err = createSession(logger, db, userID, r.Header.Get("User-Agent"), w)
		if err != nil {
			http.Error(w, fmt.Sprintf("error creating session %v", err), http.StatusInternalServerError)
			return
		}
	}
}

func handleSignup(logger *log.Logger, db *sql.DB) http.HandlerFunc {

	// The struct that represents the expected JSON body.
	type SignupRequest struct {
		FirstName string `json:"first"`
		LastName string `json:"last"`
		Email string `json:"email"`
		Password string `json:"password"`		
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
		var reqBody SignupRequest
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&reqBody); err != nil {
			http.Error(w, "Bad Request: Malformed JSON", http.StatusBadRequest)
			return
		}

		// Make sure the request body stream is closed.
		defer r.Body.Close()	

		argon := argon2.MemoryConstrainedDefaults()
		encoded, err := argon.HashEncoded([]byte(reqBody.Password))
		if err != nil {
			http.Error(w, fmt.Sprintf("error hashing password: %v", err), http.StatusInternalServerError)
		}

		stmt, err := db.Prepare("INSERT INTO users(first_name, last_name, email, password_hash) VALUES(?, ?, ?, ?)")
		if err != nil {
			http.Error(w, fmt.Sprintf("error creating prepared statement: %v", err), http.StatusInternalServerError)
			return 
		}
		defer stmt.Close()
		
		result, err := stmt.Exec(reqBody.FirstName, reqBody.LastName, reqBody.Email, string(encoded))
		if err != nil {
			http.Error(w, fmt.Sprintf("error adding user: %v", err), http.StatusInternalServerError)
			return
		}
		lastID, err := result.LastInsertId()
		if err != nil {
			http.Error(w, fmt.Sprintf("error getting id: %v", err), http.StatusInternalServerError)
			return
		}		
		logger.Printf("Added user '%s %s' (%s) %d", reqBody.FirstName, reqBody.LastName, reqBody.Email, lastID)
		
		err = createSession(logger, db, lastID, r.Header.Get("User-Agent"), w)
		if err != nil {
			http.Error(w, fmt.Sprintf("error creating session %v", err), http.StatusInternalServerError)
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
		first_name TEXT NOT NULL,
		last_name TEXT NOT NULL,
        email TEXT NOT NULL UNIQUE,
        password_hash TEXT NOT NULL,
        registration_date DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		logger.Fatalf("Error creating users table: %v", err)
	}
	logger.Println("Table 'users' created or already exists.")	

	sqlStmt = `
    PRAGMA foreign_keys = ON;

	CREATE TABLE IF NOT EXISTS sessions (
		session_cookie BLOB PRIMARY KEY UNIQUE,
        id INTEGER NOT NULL,
        creation_time DATETIME DEFAULT CURRENT_TIMESTAMP,
        expiry_time DATETIME,
        user_agent TEXT,
        FOREIGN KEY (id) REFERENCES users (id)
	);
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		logger.Fatalf("Error creating sessions table: %v", err)
	}
	logger.Println("Table 'sessions' created or already exists.")	
	
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
	mux.Handle("/api/signup", handleSignup(logger, db))	
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
