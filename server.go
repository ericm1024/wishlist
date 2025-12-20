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
	"net/mail"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ericm1024/wishlist/admin_rpc"
	"github.com/matthewhartstonge/argon2"
	grpc "google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	emptypb "google.golang.org/protobuf/types/known/emptypb"

	_ "github.com/mattn/go-sqlite3"
	"github.com/urfave/negroni"
)

type Config struct {
	DbPath          string `json:"db_path"`
	HostName        string `json:"host_name"`
	Port            string `json:"port"`
	AdminSocketPath string `json:"admin_socket_path"`
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

	maxAgeHours := 7 * 24

	// 7 day session liveness
	expiryTime := time.Now().Add(time.Duration(maxAgeHours) * time.Hour)

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

func handleSessionPost(logger *log.Logger, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
			return
		}
	}
}

func handleSessionDelete(logger *log.Logger, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
}

type User struct {
	Id        uint64 `json:"id"`
	FirstName string `json:"first"`
	LastName  string `json:"last"`
}

func handleSessionGet(logger *log.Logger, db *sql.DB) func(http.ResponseWriter, *http.Request, uint64) {
	return func(w http.ResponseWriter, r *http.Request, userId uint64) {
		user := User{Id: userId}

		stmt, err := db.Prepare("SELECT first_name,last_name FROM users WHERE id = ?")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer stmt.Close()

		err = stmt.QueryRow(userId).Scan(&user.FirstName, &user.LastName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		encoder := json.NewEncoder(w)
		if err := encoder.Encode(user); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// The struct that represents the expected JSON body.
type SignupRequest struct {
	FirstName  string `json:"first"`
	LastName   string `json:"last"`
	Email      string `json:"email"`
	Password   string `json:"password"`
	InviteCode string `json:"invite_code"`
}

func handleSignup(logger *log.Logger, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "", http.StatusUnsupportedMediaType)
			return
		}

		var reqBody SignupRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&reqBody); err != nil {
			http.Error(w, "malformed json", http.StatusBadRequest)
			return
		}

		if reqBody.FirstName == "" || reqBody.LastName == "" || reqBody.Email == "" || reqBody.Password == "" || reqBody.InviteCode == "" {
			http.Error(w, "missing fields", http.StatusBadRequest)
			return
		}

		_, err := mail.ParseAddress(reqBody.Email)
		if err != nil {
			http.Error(w, "missing fields", http.StatusBadRequest)
			return
		}

		inviteCodeBlob, err := base64.URLEncoding.DecodeString(reqBody.InviteCode)
		if err != nil {
			http.Error(w, "missing fields", http.StatusBadRequest)
			return
		}

		// Make sure the request body stream is closed.
		defer r.Body.Close()

		argon := argon2.MemoryConstrainedDefaults()
		encoded, err := argon.HashEncoded([]byte(reqBody.Password))
		if err != nil {
			http.Error(w, fmt.Sprintf("error hashing password: %v", err), http.StatusInternalServerError)
		}

		// Start a transaction
		tx, err := db.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Defer a rollback in case of errors, this will be skipped if Commit() is successful
		defer tx.Rollback()

		stmt, err := tx.Prepare("DELETE FROM invite_codes WHERE invite_code = ?")
		if err != nil {
			http.Error(w, fmt.Sprintf("error creating prepared statement: %v", err), http.StatusInternalServerError)
			return
		}
		defer stmt.Close()

		result, err := stmt.Exec(inviteCodeBlob)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		rowsDeleted, err := result.RowsAffected()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if rowsDeleted != 1 {
			http.Error(w, fmt.Sprintf("bad invite code"), http.StatusBadRequest)
			return
		}

		stmt, err = tx.Prepare("INSERT INTO users(first_name, last_name, email, password_hash) VALUES(?, ?, ?, ?)")
		if err != nil {
			http.Error(w, fmt.Sprintf("error creating prepared statement: %v", err), http.StatusInternalServerError)
			return
		}
		defer stmt.Close()

		result, err = stmt.Exec(reqBody.FirstName, reqBody.LastName, reqBody.Email, string(encoded))
		if err != nil {
			http.Error(w, fmt.Sprintf("error adding user: %v", err), http.StatusInternalServerError)
			return
		}

		err = tx.Commit()
		if err != nil {
			http.Error(w, fmt.Sprintf("error committing transaction: %v", err), http.StatusInternalServerError)
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

		response := User{uint64(lastID), reqBody.FirstName, reqBody.LastName}
		encoder := json.NewEncoder(w)
		if err := encoder.Encode(response); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

func handleWishlistGet(logger *log.Logger, db *sql.DB) func(http.ResponseWriter, *http.Request, uint64) {
	return func(w http.ResponseWriter, r *http.Request, userId uint64) {
		type WishlistEntry struct {
			Id           uint64    `json:"id"`
			Seq          uint64    `json:"seq"`
			Description  string    `json:"description"`
			Source       string    `json:"source"`
			Cost         string    `json:"cost"`
			OwnerNotes   *string   `json:"owner_notes"`
			BuyerNotes   *string   `json:"buyer_notes"`
			CreationTime time.Time `json:"creation_time"`
		}

		type WishlistGetResponse struct {
			Headers WishlistEntry   `json:"headers"`
			Entries []WishlistEntry `json:"entries"`
			User    `json:"user"`
		}

		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "", http.StatusUnsupportedMediaType)
			return
		}

		var queryUserId uint64
		userStr := r.URL.Query().Get("userId")
		if userStr != "" {
			urlUserId, err := strconv.ParseUint(userStr, 10, 64)
			if err != nil {
				http.Error(w, "missing or malformed user parameter", http.StatusBadRequest)
				return
			}
			queryUserId = urlUserId
		} else {
			queryUserId = userId
		}

		// Make sure the request body stream is closed.
		defer r.Body.Close()

		stmt, err := db.Prepare("SELECT id,sequence_number,description,source,cost,owner_notes,buyer_notes,creation_time FROM wishlist WHERE user_id = ?")
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
				&entry.OwnerNotes, &entry.BuyerNotes, &entry.CreationTime)
			logger.Printf("creation time: %v", entry.CreationTime)

			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			// requesting our own wishlist, we don't get to see the buyer notes
			if queryUserId == userId {
				entry.BuyerNotes = nil
			}
		}
		err = rows.Err()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		stmt, err = db.Prepare("SELECT first_name,last_name FROM users WHERE id = ?")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer stmt.Close()

		err = stmt.QueryRow(queryUserId).Scan(&response.User.FirstName, &response.User.LastName)
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

func handleWishlistPost(logger *log.Logger, db *sql.DB) func(http.ResponseWriter, *http.Request, uint64) {
	return func(w http.ResponseWriter, r *http.Request, id uint64) {

		type WishlistEntry struct {
			Description string `json:"description"`
			Source      string `json:"source"`
			Cost        string `json:"cost"`
			OwnerNotes  string `json:"owner_notes"`
		}

		type WishlistResponse struct {
			Id uint64 `json:"id"`
		}

		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "", http.StatusUnsupportedMediaType)
			return
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
}

func handleWishlistDelete(logger *log.Logger, db *sql.DB) func(http.ResponseWriter, *http.Request, uint64) {
	return func(w http.ResponseWriter, r *http.Request, id uint64) {
		type DeleteRequest struct {
			Ids []uint64 `json:"ids"`
		}

		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "", http.StatusUnsupportedMediaType)
			return
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
}

func handleWishlistPatch(logger *log.Logger, db *sql.DB) func(http.ResponseWriter, *http.Request, uint64) {
	return func(w http.ResponseWriter, r *http.Request, userId uint64) {
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

		if uint64(rowUserId) == userId {
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
}

func authMiddlewareNew(logger *log.Logger, db *sql.DB) func(func(http.ResponseWriter, *http.Request, uint64)) http.HandlerFunc {
	return func(nextHandler func(http.ResponseWriter, *http.Request, uint64)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			err, userId := authenticateUser(logger, db, w, r)
			if err != nil {
				return
			}
			nextHandler(w, r, userId)
		}
	}
}

func handleUsersGet(logger *log.Logger, db *sql.DB) func(http.ResponseWriter, *http.Request, uint64) {
	return func(w http.ResponseWriter, r *http.Request, userId uint64) {
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "", http.StatusUnsupportedMediaType)
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

func initDb(logger *log.Logger, dbPath string) *sql.DB {
	// Open (or create) the SQLite database file
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		logger.Fatalf("Error opening database: %v", err)
	}

	// Create users table
	sqlStmt := `
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		first_name TEXT NOT NULL CHECK(length(first_name) < 500),
		last_name TEXT NOT NULL CHECK(length(last_name) < 500),
        email TEXT NOT NULL UNIQUE CHECK(length(email) < 500),
        password_hash TEXT NOT NULL,
        registration_date DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		logger.Fatalf("Error creating users table: %v", err)
	}

	sqlStmt = `
    PRAGMA foreign_keys = ON;

	CREATE TABLE IF NOT EXISTS sessions (
		session_cookie BLOB PRIMARY KEY UNIQUE,
        id INTEGER NOT NULL,
        creation_time DATETIME DEFAULT CURRENT_TIMESTAMP,
        expiry_time DATETIME NOT NULL,
        user_agent TEXT,
        FOREIGN KEY (id) REFERENCES users (id) ON DELETE CASCADE
	);
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		logger.Fatalf("Error creating sessions table: %v", err)
	}

	sqlStmt = `
	CREATE TABLE IF NOT EXISTS wishlist (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
        sequence_number INTEGER DEFAULT 1,
        user_id INTEGER NOT NULL,
        description TEXT NOT NULL CHECK(length(description) < 2000),
        source TEXT NOT NULL CHECK(length(source) < 2000),
        cost TEXT NOT NULL CHECK(length(cost) < 2000),
        owner_notes TEXT CHECK(length(owner_notes) < 2000),
        buyer_notes TEXT CHECK(length(buyer_notes) < 2000),
        creation_time DATETIME DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
	);
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		logger.Fatalf("Error creating wishlist table: %v", err)
	}

	sqlStmt = `
	CREATE INDEX IF NOT EXISTS idx_wishlist_user ON wishlist (user_id)
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		logger.Fatalf("Error creating wishlist index: %v", err)
	}

	// user_id may be null if code was created via admin rpc
	sqlStmt = `
	CREATE TABLE IF NOT EXISTS invite_codes (
        invite_code BLOB PRIMARY KEY UNIQUE,
        user_id INTEGER,
        creation_time DATETIME DEFAULT CURRENT_TIMESTAMP,
        expiry_time DATETIME NOT NULL,
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
	);
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		logger.Fatalf("Error creating invite_codes table: %v", err)
	}

	sqlStmt = `
	CREATE INDEX IF NOT EXISTS idx_invite_codes_user ON invite_codes (user_id)
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		logger.Fatalf("Error creating wishlist index: %v", err)
	}

	return db
}

func addRoutes(
	mux *http.ServeMux,
	logger *log.Logger,
	config *Config,
	db *sql.DB,
) {
	authMiddleware := authMiddlewareNew(logger, db)

	mux.Handle("GET /api/session", authMiddleware(handleSessionGet(logger, db)))
	mux.Handle("POST /api/session", handleSessionPost(logger, db))
	mux.Handle("DELETE /api/session", handleSessionDelete(logger, db))

	mux.Handle("POST /api/signup", handleSignup(logger, db))

	mux.Handle("GET /api/wishlist", authMiddleware(handleWishlistGet(logger, db)))
	mux.Handle("POST /api/wishlist", authMiddleware(handleWishlistPost(logger, db)))
	mux.Handle("DELETE /api/wishlist", authMiddleware(handleWishlistDelete(logger, db)))
	mux.Handle("PATCH /api/wishlist", authMiddleware(handleWishlistPatch(logger, db)))

	mux.Handle("GET /api/users", authMiddleware(handleUsersGet(logger, db)))
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

type adminGrpcServer struct {
	admin_rpc.UnimplementedWishlistAdminServer
	Logger *log.Logger
	Db     *sql.DB
}

func generateInviteCodeHelper(db *sql.DB) ([]byte, error) {
	stmt, err := db.Prepare("INSERT INTO invite_codes(invite_code, expiry_time) VALUES(?, ?)")
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	// Note that no error handling is necessary, as Read always succeeds.
	inviteCode := make([]byte, 32)
	rand.Read(inviteCode)

	// invite codes good for 7 days
	expiryTime := time.Now().Add(time.Duration(7*24) * time.Hour)

	_, err = stmt.Exec(inviteCode, expiryTime)
	if err != nil {
		return nil, err
	}
	return inviteCode, nil
}

func (s *adminGrpcServer) GenerateInviteCode(ctx context.Context, in *emptypb.Empty) (*admin_rpc.IvniteCodeReply, error) {

	inviteCode, err := generateInviteCodeHelper(s.Db)
	if err != nil {
		return nil, err
	}

	return &admin_rpc.IvniteCodeReply{Code: base64.URLEncoding.EncodeToString(inviteCode)}, nil
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
	config := Config{DbPath: "wishlist.db", HostName: "localhost", Port: "80",
		AdminSocketPath: "wishlist_admin.sock"}

	err = json.Unmarshal(configFile, &config)
	if err != nil {
		log.Fatalf("Error unmarshaling config JSON: %v", err)
	}

	if err := os.RemoveAll(config.AdminSocketPath); err != nil {
		log.Fatal(err)
	}

	// do this early since we have to muck with the umask
	oldUmask := syscall.Umask(0077) // Sets permissions to 0700 (owner rwx)
	lis, err := net.Listen("unix", config.AdminSocketPath)
	syscall.Umask(oldUmask) // Restore origial umask
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	db := initDb(logger, config.DbPath)
	defer db.Close()

	srv := NewServer(logger, &config, db)
	httpServer := &http.Server{
		Addr:    net.JoinHostPort(config.HostName, config.Port),
		Handler: srv,
	}
	go func() {
		logger.Printf("http listening on %s\n", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "error listening and serving: %s\n", err)
		}
	}()
	var wg sync.WaitGroup
	wg.Add(2)
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

	grpcServer := grpc.NewServer()
	admin_rpc.RegisterWishlistAdminServer(grpcServer, &adminGrpcServer{Logger: logger, Db: db})
	reflection.Register(grpcServer)
	go func() {
		log.Printf("grpc server listening at %v", lis.Addr())
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		<-ctx.Done()
		// xxx: try to gracefully stop?
		grpcServer.Stop()
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
