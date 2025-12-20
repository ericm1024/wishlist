package main

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSignup(t *testing.T) {

	type testCase struct {
		name        string
		body        string
		code        int
		method      string
		contentType string
	}

	tests := []testCase{
		{
			name: "bad content type",
			body: `{
                             "first": "joe",
                             "last": "cool",
                             "email": "joecool@gmail.com",
                             "password": "mypassword"
                        }`,
			method:      "POST",
			contentType: "text/plain",
			code:        http.StatusUnsupportedMediaType,
		},
		{
			name: "extra field",
			body: `{
                             "foo": "bar"
                        }`,
			method: "POST",
			code:   http.StatusBadRequest,
		},
		{
			name: "malformed json",
			body: `{{
                        }`,
			method: "POST",
			code:   http.StatusBadRequest,
		},
		{
			name: "missing first",
			body: `{
                             "last": "cool",
                             "email": "joecool@gmail.com",
                             "password": "mypassword"
                        }`,
			method: "POST",
			code:   http.StatusBadRequest,
		},
		{
			name: "missing last",
			body: `{
                             "first": "joe",
                             "email": "joecool@gmail.com",
                             "password": "mypassword"
                        }`,
			method: "POST",
			code:   http.StatusBadRequest,
		},
		{
			name: "missing email",
			body: `{
                             "first": "joe",
                             "last": "cool",
                             "password": "mypassword"
                        }`,
			method: "POST",
			code:   http.StatusBadRequest,
		},
		{
			name: "missing password",
			body: `{
                             "first": "joe",
                             "last": "cool",
                             "email": "joecool@gmail.com",
                        }`,
			method: "POST",
			code:   http.StatusBadRequest,
		},
		{
			name: "bad email",
			body: `{
                             "first": "joe",
                             "last": "cool",
                             "email": "this is not an email address",
                             "password": "mypassword"
                        }`,
			method: "POST",
			code:   http.StatusBadRequest,
		},
		{
			name: "basic",
			body: `{
                             "first": "joe",
                             "last": "cool",
                             "email": "joecool@gmail.com",
                             "password": "mypassword"
                        }`,
			method: "POST",
		},
		{
			name: "duplicate user",
			body: `{
                             "first": "joe",
                             "last": "cool",
                             "email": "joecool@gmail.com",
                             "password": "mypassword"
                        }`,
			method: "POST",
			code:   http.StatusInternalServerError,
		},
		{
			name: "long first name",
			body: `{
                             "first": "` + strings.Repeat("A", 1000) + `",
                             "last": "cool",
                             "email": "user2@gmail.com",
                             "password": "mypassword"
                        }`,
			method: "POST",
			code:   http.StatusInternalServerError,
		},
		{
			name: "long last name",
			body: `{
                             "first": "joe",
                             "last": "` + strings.Repeat("A", 1000) + `",
                             "email": "user3@gmail.com",
                             "password": "mypassword"
                        }`,
			method: "POST",
			code:   http.StatusInternalServerError,
		},
		{
			name: "long email",
			body: `{
                             "first": "joe",
                             "last": "cool",
                             "email": "` + strings.Repeat("A", 1000) + `@gmail.com",
                             "password": "mypassword"
                        }`,
			method: "POST",
			code:   http.StatusInternalServerError,
		},
	}

	logger := log.Default()
	db := initDb(logger, ":memory:")
	defer db.Close()
	handler := handleSignup(logger, db)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.code == 0 {
				tc.code = http.StatusOK
			}
			if tc.method == "" {
				tc.method = "GET"
			}
			if tc.contentType == "" {
				tc.contentType = "application/json"
			}

			// inject a valid invite code
			bodyCopy := tc.body
			var body map[string]string
			err := json.Unmarshal([]byte(bodyCopy), &body)
			if err == nil {
				inviteCode, err := generateInviteCodeHelper(db)
				if err != nil {
					t.Errorf("failed to generate invite code: %v", err)
					return
				}
				body["invite_code"] = base64.URLEncoding.EncodeToString(inviteCode)
				bytes, err := json.Marshal(body)
				if err != nil {
					t.Errorf("failed to re-marshal body: %v", err)
					return
				}
				bodyCopy = string(bytes)
			}

			req := httptest.NewRequest(tc.method, "/signup", strings.NewReader(bodyCopy))
			req.Header.Set("Content-Type", tc.contentType)
			rr := httptest.NewRecorder()

			handler(rr, req)
			if rr.Result().StatusCode != tc.code {
				t.Errorf("unexpected status %d (expected %d)", rr.Result().StatusCode,
					tc.code)
			}
		})
	}
}

// TODO: test invite code reuse, test that invite codes are not used up by invalid requests
