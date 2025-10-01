package main

import (
    "fmt"
    "log"
    "net/http"
	"database/sql"

	_ "github.com/mattn/go-sqlite3" // Import the SQLite driver
)


func helloHandler(w http.ResponseWriter, r *http.Request) {
    fmt.Fprintf(w, "Hello, world!")
}

func main() {
	// Open (or create) the SQLite database file
	db, err := sql.Open("sqlite3", "./example.db")
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}
	defer db.Close() // Ensure the database connection is closed when main exits

	// Create users table
	sqlStmt := `
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL
	);
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		log.Fatalf("Error creating table: %v", err)
	}
	log.Println("Table 'users' created or already exists.")	

	// Insert data
	stmt, err := db.Prepare("INSERT INTO users(name) VALUES(?)")
	if err != nil {
		log.Fatalf("Error preparing insert statement: %v", err)
	}
	defer stmt.Close()

	_, err = stmt.Exec("Eric")
	if err != nil {
		log.Fatalf("Error inserting data for Eric: %v", err)
	}
	log.Println("Inserted Eric.")

	// Query data
	rows, err := db.Query("SELECT id, name FROM users")
	if err != nil {
		log.Fatalf("Error querying data: %v", err)
	}
	defer rows.Close()
	
	log.Println("Users in the database:")
	for rows.Next() {
		var id int
		var name string
		err = rows.Scan(&id, &name)
		if err != nil {
			log.Fatalf("Error scanning row: %v", err)
		}
		log.Printf("ID: %d, Name: %s\n", id, name)
	}
	err = rows.Err()
	if err != nil {
		log.Fatalf("Error iterating rows: %v", err)
	}
	
    http.HandleFunc("/", helloHandler)

    //fmt.Println("Starting server on port 8080...")
    //log.Fatal(http.ListenAndServe(":8080", nil))
}
