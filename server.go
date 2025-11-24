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
	"io/ioutil"
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
	DbPath   string `json:"db_path"`
	HostName string `json:"host_name"`
	Port     string `json:"port"`
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
func createSession(logger *log.Logger, db *sql.DB, userId int64, userAgent string, w http.ResponseWriter) error {
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
		Name:     sessionCookieKey,
		Value:    base64.URLEncoding.EncodeToString(sessionCookie),
		Expires:  expiryTime,
		Secure:   true,
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
		Email    string `json:"email"`
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

	stmt, err := db.Prepare("SELECT password_hash,id,first_name,last_name FROM users WHERE email = ?")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer stmt.Close()

	var passwordHash string
	var userId int64
	var firstName string
	var lastName string
	err = stmt.QueryRow(reqBody.Email).Scan(&passwordHash, &userId, &firstName, &lastName)
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

	err = createSession(logger, db, userId, r.Header.Get("User-Agent"), w)
	if err != nil {
		http.Error(w, fmt.Sprintf("error creating session %v", err), http.StatusInternalServerError)
		return
	}

	response := User{uint64(userId), firstName, lastName}
	encoder := json.NewEncoder(w)
	if err := encoder.Encode(response); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
	Id        uint64 `json:"id"`
	FirstName string `json:"first"`
	LastName  string `json:"last"`
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
		LastName  string `json:"last"`
		Email     string `json:"email"`
		Password  string `json:"password"`
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
	type WishlistEntry struct {
		Id          uint64  `json:"id"`
		Seq         uint64  `json:"seq"`
		Description string  `json:"description"`
		Source      string  `json:"source"`
		Cost        string  `json:"cost"`
		OwnerNotes  *string `json:"owner_notes"`
		BuyerNotes  *string `json:"buyer_notes"`
	}

	type WishlistGetResponse struct {
		Headers WishlistEntry   `json:"headers"`
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

	stmt, err := db.Prepare("SELECT id,sequence_number,description,source,cost,owner_notes,buyer_notes FROM wishlist WHERE user_id = ?")
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

		err = rows.Scan(&entry.Id, &entry.Seq, &entry.Description, &entry.Source, &entry.Cost,
			&entry.OwnerNotes, &entry.BuyerNotes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// requesting our own wishlist, we don't get to see the buyer notes
		if queryUserId == id {
			entry.BuyerNotes = nil
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

func handleWishlistPost(id uint64, logger *log.Logger, db *sql.DB, w http.ResponseWriter, r *http.Request) {
	type WishlistEntry struct {
		Description string `json:"description"`
		Source      string `json:"source"`
		Cost        string `json:"cost"`
		OwnerNotes  string `json:"owner_notes"`
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

	args := make([]interface{}, len(reqBody.Ids)+1)
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

func handleWishlistPatch(id uint64, logger *log.Logger, db *sql.DB, w http.ResponseWriter, r *http.Request) {
	type WishlistPatch struct {
		Id          uint64  `json:"id"`
		Seq         uint64  `json:"seq"`
		Description *string `json:"description"`
		Source      *string `json:"source"`
		Cost        *string `json:"cost"`
		OwnerNotes  *string `json:"owner_notes"`
		BuyerNotes  *string `json:"buyer_notes"`
	}

	var req WishlistPatch
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "Bad Request: Malformed JSON", http.StatusBadRequest)
		return
	}

	// Make sure the request body stream is closed.
	defer r.Body.Close()

	if req.Id == 0 || req.Seq == 0 {
		http.Error(w, "missing id or seq", http.StatusBadRequest)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Defer a rollback in case of errors, this will be skipped if Commit() is successful
	defer tx.Rollback()

	// Prepare a statement for insertion within the transaction
	selectStmt, err := tx.Prepare("SELECT user_id,sequence_number FROM wishlist WHERE id == ?")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer selectStmt.Close()

	var rowUserId int64
	var sequenceNumber int64
	err = selectStmt.QueryRow(req.Id).Scan(&rowUserId, &sequenceNumber)
	if err != nil {
		http.Error(w, "error loading row", http.StatusInternalServerError)
		return
	}

	if uint64(sequenceNumber) != req.Seq {
		http.Error(w, fmt.Sprintf("client seq %d does not match server seq %d, try again",
			req.Seq, sequenceNumber), http.StatusConflict)
		return
	}

	if uint64(rowUserId) == id {
		if req.BuyerNotes != nil {
			http.Error(w, "wishlist owner can not edit buyer notes", http.StatusBadRequest)
			return
		}
		if req.Description == nil && req.Source == nil && req.Cost == nil && req.OwnerNotes == nil {
			http.Error(w, "must provide something to patch", http.StatusBadRequest)
			return
		}
	} else {
		if req.Description != nil || req.Source != nil || req.Cost != nil || req.OwnerNotes != nil {
			http.Error(w, "non-owner can only edit buyer notes", http.StatusBadRequest)
			return
		}
		if req.BuyerNotes == nil {
			http.Error(w, "must provide something to patch", http.StatusBadRequest)
			return
		}
	}

	var fields = []struct {
		RequestField *string
		DbColumn     string
	}{
		{req.Description, "description"},
		{req.Source, "source"},
		{req.Cost, "cost"},
		{req.OwnerNotes, "owner_notes"},
		{req.BuyerNotes, "buyer_notes"},
	}

	var arguments []interface{}
	var fieldsToSet []string
	for _, mapping := range fields {
		if mapping.RequestField != nil {
			arguments = append(arguments, *mapping.RequestField)
			fieldsToSet = append(fieldsToSet, fmt.Sprintf("%s = ?", mapping.DbColumn))
		}
	}
	fieldsToSet = append(fieldsToSet, "sequence_number = ?")
	arguments = append(arguments, req.Seq+1)

	arguments = append(arguments, req.Id)

	preparedStr := fmt.Sprintf("UPDATE wishlist SET %s WHERE id = ?", strings.Join(fieldsToSet, ", "))
	logger.Printf("update statement: %s", preparedStr)
	updateStmt, err := tx.Prepare(preparedStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer updateStmt.Close()

	_, err = updateStmt.Exec(arguments...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = tx.Commit()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		} else if r.Method == http.MethodPatch {
			handleWishlistPatch(id, logger, db, w, r)
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
        sequence_number INTEGER DEFAULT 1,
        user_id INTEGER NOT NULL,
        description TEXT NOT NULL,
        source TEXT NOT NULL,
        cost TEXT NOT NULL,
        owner_notes TEXT,
        buyer_notes TEXT,
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

	return db
}

func addRoutes(
	mux *http.ServeMux,
	logger *log.Logger,
	config *Config,
	db *sql.DB,
) {
	mux.Handle("/api/session", handleSession(logger, db))
	mux.Handle("/api/signup", handleSignup(logger, db))
	mux.Handle("/api/wishlist", handleWishlist(logger, db))
	mux.Handle("/api/users", handleUsers(logger, db))
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
	db *sql.DB,
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

	configFile, err := ioutil.ReadFile("config.json")
	if err != nil {
		logger.Fatalf("Error reading config file: %v", err)
	}

	// default values
	config := Config{DbPath: "wishlist.db", HostName: "localhost", Port: "80"}

	err = json.Unmarshal(configFile, &config)
	if err != nil {
		log.Fatalf("Error unmarshaling config JSON: %v", err)
	}

	db := initDb(logger, &config)

	srv := NewServer(logger, &config, db)
	httpServer := &http.Server{
		Addr:    net.JoinHostPort(config.HostName, config.Port),
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
		shutdownCtx, cancel := context.WithTimeout(shutdownCtx, 10*time.Second)
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
