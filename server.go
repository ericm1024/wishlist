// TODOs
// * use DisallowUnknownFields and ensure non-nil fields for all json requests
// * client & server side input validation for signup form
// * client & server side input validation for login form
// * normalize password strings? https://stackoverflow.com/a/66899076
// * limit field sizes for all client-controlled fields
// * remove expired sessions
// * resend session cookies periodically
// * top-level middleware to do 'defer r.Body.Close()' bullshit
// * helper function for request decoding
// * Content-Security-Policy ?

package main

import (
    "context"
    "crypto/rand"	
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
    "fmt"
    "io"
    "log"
    "net"					
    "net/http"
    "os"			
    "os/signal"
    "strconv"
	"strings"
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

func extractCookie(r *http.Request) []byte {
	sessionCookie, err := r.Cookie(sessionCookieKey)
	if err != nil {
		return nil
	}

	binaryCookie, err := base64.URLEncoding.DecodeString(sessionCookie.Value)
	if err != nil {
		return nil
	}
	return binaryCookie
}

func authenticateUser(logger *log.Logger, db *sql.DB, w http.ResponseWriter, r *http.Request) (error, uint64) {
	cookie := extractCookie(r)
	if cookie == nil {
		http.Error(w, "missing session cookie", http.StatusUnauthorized)
		return errors.New("missing session cookie"), 0
	}

	stmt, err := db.Prepare("SELECT expiry_time, id FROM sessions WHERE session_cookie = ?")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err, 0
	}
	defer stmt.Close()

	var expiryTime time.Time
	var id int64
	err = stmt.QueryRow(cookie).Scan(&expiryTime, &id)
	if err != nil {
		// XXX: differentiate no DB entry vs "something weird"
		http.Error(w, "no cookie in db", http.StatusUnauthorized)
		return err, 0
	}

	if expiryTime.Before(time.Now()) {
		http.Error(w, "expired cookie", http.StatusUnauthorized)
		return errors.New("cookie expired"), 0
	}

	return nil, uint64(id)
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

func handleSessionPost(logger *log.Logger, db *sql.DB, w http.ResponseWriter, r *http.Request) {
	// The struct that represents the expected JSON body.
	type LoginRequest struct {
		Email string `json:"email"`
		Password string `json:"password"`
	}

	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "", http.StatusUnsupportedMediaType)
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

func handleSessionDelete(logger *log.Logger, db *sql.DB, w http.ResponseWriter, r *http.Request) {
	cookie := extractCookie(r)
	if cookie == nil {
		return
	}
	
	stmt, err := db.Prepare("DELETE FROM sessions WHERE session_cookie = ? RETURNING id ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer stmt.Close()

	var id int64
	err = stmt.QueryRow(cookie).Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err != sql.ErrNoRows {
		logger.Printf("deleting session for user %d", id)	
	}
}

type User struct {
	Id uint64 `json:"id"`
	FirstName string `json:"first"`
	LastName string `json:"last"`
}

func handleSessionGet(logger *log.Logger, db *sql.DB, w http.ResponseWriter, r *http.Request) {
	var user User
	err, id := authenticateUser(logger, db, w, r)
	if err != nil {
		return
	}
	user.Id = id

	stmt, err := db.Prepare("SELECT first_name,last_name FROM users WHERE id = ?")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer stmt.Close()

	err = stmt.QueryRow(id).Scan(&user.FirstName, &user.LastName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	encoder := json.NewEncoder(w)
	if err := encoder.Encode(user); err != nil {    
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}	
}

func handleSession(logger *log.Logger, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {		
		if r.Method == http.MethodPost {
			handleSessionPost(logger, db, w, r)
		} else if r.Method == http.MethodDelete {
			handleSessionDelete(logger, db, w, r)
		} else if r.Method == http.MethodGet {
			handleSessionGet(logger, db, w, r)
		} else {
			http.Error(w, "", http.StatusMethodNotAllowed)
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

func handleWishlistGet(id uint64, logger *log.Logger, db *sql.DB, w http.ResponseWriter, r *http.Request) {
	type Comment struct {
		Id uint64 `json:"id"`
		First string `json:"first"`
		Last string `json:"last"`
		Comment string `json:"comment"`
	}
	
	type WishlistEntry struct {
		Id uint64 `json:"id"`
		Description string `json:"description"`
		Source string `json:"source"`
		Cost string `json:"cost"`
		Comments []Comment `json:"comments"`
	}
	
	type WishlistGetResponse struct {
		Headers WishlistEntry `json:"headers"`
		Entries []WishlistEntry `json:"entries"`
	}

	var queryUserId uint64
	
	userStr := r.URL.Query().Get("userId")
	if userStr != "" {
		userId, err := strconv.ParseUint(userStr, 10, 64)
		if err != nil {
			http.Error(w, "missing or malformed user parameter", http.StatusBadRequest)
			return
		}
		queryUserId = userId
	} else {
		queryUserId = id
	}
		
	// Make sure the request body stream is closed.
	defer r.Body.Close()

	stmt, err := db.Prepare("SELECT id,description,source,cost FROM wishlist WHERE user_id = ?")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer stmt.Close()

	rows, err := stmt.Query(queryUserId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var response WishlistGetResponse
	for rows.Next() {
		response.Entries = append(response.Entries, WishlistEntry{})
		entry := &response.Entries[len(response.Entries)-1]
		
		err = rows.Scan(&entry.Id, &entry.Description, &entry.Source, &entry.Cost)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	err = rows.Err()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}


	if queryUserId != id {
		stmt.Close()
		
		// XXX: use a txn to get a consistent view with the previous select
		
		stmt, err := db.Prepare(`
            SELECT wishlist.id, comments.id, users.first_name, users.last_name, comments.comment
            FROM wishlist
            INNER JOIN comments on comments.wishlist_item_id = wishlist.id
            INNER JOIN users on comments.user_id = users.id where wishlist.user_id == ?`)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer stmt.Close()

		rows, err := stmt.Query(queryUserId)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var wishlistId uint64
			var comment Comment
			err = rows.Scan(&wishlistId, &comment.Id, &comment.First, &comment.Last, &comment.Comment)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			// XXX: N^2, return a map?
			for idx, _ := range response.Entries {
				var entry = &response.Entries[idx]
				if entry.Id == wishlistId {
					entry.Comments = append(entry.Comments, comment)
				}
			}
		}
		err = rows.Err()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	
	// Encode the data and write it to the response
	encoder := json.NewEncoder(w)
	if err := encoder.Encode(response); err != nil {                  
        http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleWishlistPost(id uint64, logger *log.Logger, db *sql.DB, w http.ResponseWriter, r *http.Request) {
	type WishlistEntry struct {
		Description string `json:"description"`
		Source string `json:"source"`
		Cost string `json:"cost"`
		OwnerNotes string `json:"owner_notes"`
	}

	type WishlistResponse struct {
		Id uint64 `json:"id"`
	}

	var reqBody WishlistEntry
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reqBody); err != nil {
		http.Error(w, "Bad Request: Malformed JSON", http.StatusBadRequest)
		return
	}
		
	// Make sure the request body stream is closed.
	defer r.Body.Close()

	stmt, err := db.Prepare("INSERT INTO wishlist(user_id, description, source, cost, owner_notes) VALUES(?, ?, ?, ?, ?)")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer stmt.Close()

	result, err := stmt.Exec(id, reqBody.Description, reqBody.Source, reqBody.Cost, reqBody.OwnerNotes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	lastID, err := result.LastInsertId()
	if err != nil {
		http.Error(w, fmt.Sprintf("error getting id: %v", err), http.StatusInternalServerError)
		return
	}

	response := WishlistResponse{Id: uint64(lastID)}

	// Encode the data and write it to the response
	encoder := json.NewEncoder(w)
	if err := encoder.Encode(response); err != nil {                  
        http.Error(w, err.Error(), http.StatusInternalServerError)
	}	
}

func handleWishlistDelete(id uint64, logger *log.Logger, db *sql.DB, w http.ResponseWriter, r *http.Request) {
	type DeleteRequest struct {
		Ids []uint64 `json:"ids"`
	}

	var reqBody DeleteRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reqBody); err != nil {
		http.Error(w, "Bad Request: Malformed JSON", http.StatusBadRequest)
		return
	}

	// Make sure the request body stream is closed.
	defer r.Body.Close()

	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Defer a rollback in case of errors, this will be skipped if Commit() is successful
	defer tx.Rollback()	

	// Generate the correct number of placeholders (?, ?, ?...)
	placeholders := make([]string, len(reqBody.Ids))
	for i := range reqBody.Ids {
		placeholders[i] = "?"
	}
	placeholdersStr := strings.Join(placeholders, ", ")
	
	// Prepare a statement for insertion within the transaction
	selectStmt, err := tx.Prepare(
		fmt.Sprintf("SELECT COUNT(*) FROM wishlist WHERE id IN (%s) AND user_id != ?", placeholdersStr))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer selectStmt.Close() // Close the statement when done

	args := make([]interface{}, len(reqBody.Ids) + 1)
	for i, id := range reqBody.Ids {
		args[i] = id
	}
	args[len(reqBody.Ids)] = id
	
	var count uint
	selectStmt.QueryRow(args...).Scan(&count)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if count != 0 {
		http.Error(w, "Attempt to delete wishlist rows not owned by user", http.StatusUnauthorized)
		return
	}
	
	deleteStmt, err := tx.Prepare(
		fmt.Sprintf("DELETE FROM wishlist WHERE id IN (%s) AND user_id == ?", placeholdersStr))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer deleteStmt.Close()

	result, err := deleteStmt.Exec(args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = tx.Commit()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	rowsAffected, err := result.RowsAffected()
	if err != nil && rowsAffected == 0 {
		http.Error(w, "non-existent row", http.StatusNotFound)
		return
	}
}

func handleWishlist(logger *log.Logger, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err, id := authenticateUser(logger, db, w, r)
		if err != nil {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "", http.StatusUnsupportedMediaType)
			return
		}
		
		if r.Method == http.MethodGet {
			handleWishlistGet(id, logger, db, w, r)
		} else if r.Method == http.MethodPost {
			handleWishlistPost(id, logger, db, w, r)
		} else if r.Method == http.MethodDelete {
			handleWishlistDelete(id, logger, db, w, r)
		} else {
			http.Error(w, "", http.StatusMethodNotAllowed)
			return
		}
	}
}

func handleUsers(logger *log.Logger, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err, _ := authenticateUser(logger, db, w, r)
		if err != nil {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "", http.StatusUnsupportedMediaType)
			return
		}
		
		if r.Method != http.MethodGet {
			http.Error(w, "", http.StatusMethodNotAllowed)
			return
		}
		
		type UsersResponse struct {
			Entries []User `json:"users"`
		}
		
		stmt, err := db.Prepare("SELECT id,first_name,last_name FROM users")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer stmt.Close()
		
		rows, err := stmt.Query()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		
		var response UsersResponse
		for rows.Next() {
			response.Entries = append(response.Entries, User{})
			entry := &response.Entries[len(response.Entries)-1]

			err = rows.Scan(&entry.Id, &entry.FirstName, &entry.LastName)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		err = rows.Err()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Encode the data and write it to the response
		encoder := json.NewEncoder(w)
		if err := encoder.Encode(response); err != nil {    
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func handleCommentPost(id uint64, logger *log.Logger, db *sql.DB, w http.ResponseWriter, r *http.Request) {
	type CommentRequest struct {
		// wishlist row id
		Id uint64 `json:"id"`
		Comment string `json:"comment"`
	}

	var reqBody CommentRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reqBody); err != nil {
		http.Error(w, "Bad Request: Malformed JSON", http.StatusBadRequest)
		return
	}
	
	// Make sure the request body stream is closed.
	defer r.Body.Close()

	if reqBody.Comment == "" || reqBody.Id == 0 {
		http.Error(w, "Bad Request: Malformed JSON", http.StatusBadRequest)
		return
	}
	
	// verify user isn't trying to comment on their own wishlist
	stmt, err := db.Prepare("SELECT user_id FROM wishlist WHERE id = ?")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer stmt.Close()

	var wishlistRowUser int64
	err = stmt.QueryRow(reqBody.Id).Scan(&wishlistRowUser)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if uint64(wishlistRowUser) == id {
		http.Error(w, "can't comment on your own wishlist", http.StatusUnauthorized)
		return
	}
	stmt.Close()

	// no TOCU race with the check above because wishlist row ownership is imutable,
	// so no need for a txn.
	
	stmt, err = db.Prepare("INSERT INTO comments(wishlist_item_id, user_id, comment) VALUES(?, ?, ?)")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer stmt.Close()
	
	_, err = stmt.Exec(reqBody.Id, id, reqBody.Comment)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func handleCommentDelete(id uint64, logger *log.Logger, db *sql.DB, w http.ResponseWriter, r *http.Request) {
	type DeleteRequest struct {
		Id uint64 `json:"id"`
	}

	var reqBody DeleteRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reqBody); err != nil {
		http.Error(w, "Bad Request: Malformed JSON", http.StatusBadRequest)
		return
	}

	// Make sure the request body stream is closed.
	defer r.Body.Close()
	
	stmt, err := db.Prepare("DELETE FROM comments WHERE id == ? AND user_id == ?")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer stmt.Close()

	result, err := stmt.Exec(reqBody.Id, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	rowsAffected, err := result.RowsAffected()
	if err != nil && rowsAffected == 0 {
		// XXX: be more precise with the error here
		http.Error(w, "user id mismatch", http.StatusUnauthorized)
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func handleComments(logger *log.Logger, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err, id := authenticateUser(logger, db, w, r)
		if err != nil {
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "", http.StatusUnsupportedMediaType)
			return
		}
		
		if r.Method == http.MethodPost {
			handleCommentPost(id, logger, db, w, r)
		} else if r.Method == http.MethodDelete {
			handleCommentDelete(id, logger, db, w, r)
		} else {
			http.Error(w, "", http.StatusMethodNotAllowed)
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
        FOREIGN KEY (id) REFERENCES users (id) ON DELETE CASCADE
	);
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		logger.Fatalf("Error creating sessions table: %v", err)
	}
	logger.Println("Table 'sessions' created or already exists.")	

	sqlStmt = `
	CREATE TABLE IF NOT EXISTS wishlist (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
        user_id INTEGER NOT NULL,
        description TEXT NOT NULL,
        source TEXT NOT NULL,
        cost TEXT NOT NULL,
        creation_time DATETIME DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
	);
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		logger.Fatalf("Error creating wishlist table: %v", err)
	}
	logger.Println("Table 'wishlist' created or already exists.")	

	sqlStmt = `
	CREATE INDEX IF NOT EXISTS idx_wishlist_user ON wishlist (user_id)
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		logger.Fatalf("Error creating wishlist index: %v", err)
	}
	logger.Println("Index 'idx_wishlist_user' created or already exists.")	
	
	sqlStmt = `
	CREATE TABLE IF NOT EXISTS comments (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
        wishlist_item_id INTEGER NOT NULL,
        user_id INTEGER NOT NULL,
        comment TEXT NOT NULL,
        creation_time DATETIME DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (wishlist_item_id) REFERENCES wishlist (id) ON DELETE CASCADE,
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
	);
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		logger.Fatalf("Error creating comments table: %v", err)
	}
	logger.Println("Table 'comments' created or already exists.")	

	sqlStmt = `
	CREATE INDEX IF NOT EXISTS idx_comments_wishlist_item ON comments (wishlist_item_id)
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		logger.Fatalf("Error creating comments index: %v", err)
	}
	logger.Println("Index 'idx_comments_wishlist_item' created or already exists.")	

	
	return db
}

func addRoutes(
	mux                 *http.ServeMux,
	logger              *log.Logger,
	config              *Config,
	db                  *sql.DB,
) {
	mux.Handle("/api/session", handleSession(logger, db))
	mux.Handle("/api/signup", handleSignup(logger, db))
	mux.Handle("/api/wishlist", handleWishlist(logger, db))
	mux.Handle("/api/users", handleUsers(logger, db))
	mux.Handle("/api/comments", handleComments(logger, db))
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
