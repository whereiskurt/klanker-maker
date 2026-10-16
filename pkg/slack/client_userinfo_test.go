package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUserInfo_ParsesEmailDeletedBot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users.info" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("user") != "U0KURT" {
			t.Errorf("form user=%q err=%v", r.Form.Get("user"), err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"user":{"id":"U0KURT","deleted":true,"is_bot":false,"profile":{"email":"Kurt@Example.com"}}}`))
	}))
	defer srv.Close()
	c := NewClient("xoxb-test", srv.Client())
	c.SetBaseURL(srv.URL)
	u, err := c.UserInfo(context.Background(), "U0KURT")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != "U0KURT" || u.Email != "kurt@example.com" || !u.Deleted || u.IsBot {
		t.Fatalf("got %+v", u)
	}
}

func TestUserInfo_SlackErrorIsReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":false,"error":"user_not_found"}`))
	}))
	defer srv.Close()
	c := NewClient("xoxb-test", srv.Client())
	c.SetBaseURL(srv.URL)
	if _, err := c.UserInfo(context.Background(), "UNOPE"); err == nil {
		t.Fatal("want error")
	}
}
