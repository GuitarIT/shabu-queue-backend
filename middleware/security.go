package middleware

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net/http"
	"sync"
	"time"

	"shabu-queue/models"
)

type contextKey string

const (
	UserContextKey    contextKey = "currentUser"
	SessionContextKey contextKey = "currentSession"
)

const SessionCookieName = "shabu_session_id"

// Session เก็บข้อมูลเซสชันของผู้ใช้ฝั่ง Server (ป้องกันการปลอมแปลง Cookie ตาม OWASP A07)
type Session struct {
	ID           string
	User         models.User
	CSRFToken    string
	FlashSuccess string
	FlashError   string
	ExpiresAt    time.Time
}

var (
	sessionsMu sync.RWMutex
	sessions   = make(map[string]*Session)
)

func generateRandomToken(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateSession สร้างเซสชันใหม่เมื่อ Login สำเร็จ และตั้งค่า HttpOnly Cookie
func CreateSession(w http.ResponseWriter, user models.User) (*Session, error) {
	sessionID, err := generateRandomToken(32)
	if err != nil {
		return nil, err
	}
	csrfToken, err := generateRandomToken(24)
	if err != nil {
		return nil, err
	}

	sess := &Session{
		ID:        sessionID,
		User:      user,
		CSRFToken: csrfToken,
		ExpiresAt: time.Now().Add(12 * time.Hour),
	}

	sessionsMu.Lock()
	sessions[sessionID] = sess
	sessionsMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true, // OWASP A07: ป้องกัน JavaScript เข้าถึง Session Cookie (XSS protection)
		SameSite: http.SameSiteLaxMode,
		MaxAge:   12 * 3600,
	})

	return sess, nil
}

// GetSession ดึงข้อมูลเซสชันจาก Request
func GetSession(r *http.Request) *Session {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil
	}

	sessionsMu.RLock()
	sess, exists := sessions[cookie.Value]
	sessionsMu.RUnlock()

	if !exists {
		return nil
	}
	if time.Now().After(sess.ExpiresAt) {
		DestroySessionByID(cookie.Value)
		return nil
	}
	return sess
}

// DestroySession ลบเซสชันออกจากหน่วยความจำและล้าง Cookie
func DestroySession(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(SessionCookieName)
	if err == nil && cookie.Value != "" {
		DestroySessionByID(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func DestroySessionByID(id string) {
	sessionsMu.Lock()
	delete(sessions, id)
	sessionsMu.Unlock()
}

// SetFlash ตั้งค่าข้อความแจ้งเตือนแบบแสดงครั้งเดียวใน Session
func SetFlash(r *http.Request, successMsg, errorMsg string) {
	sess := GetSession(r)
	if sess == nil {
		return
	}
	sessionsMu.Lock()
	sess.FlashSuccess = successMsg
	sess.FlashError = errorMsg
	sessionsMu.Unlock()
}

// PopFlash ดึงและล้างข้อความแจ้งเตือนใน Session
func PopFlash(sess *Session) (string, string) {
	if sess == nil {
		return "", ""
	}
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	s, e := sess.FlashSuccess, sess.FlashError
	sess.FlashSuccess = ""
	sess.FlashError = ""
	return s, e
}

// ValidateCSRF ตรวจสอบโทเค็นป้องกัน CSRF ในทุกคำสั่ง POST (OWASP A01 / A07)
func ValidateCSRF(r *http.Request, sess *Session) bool {
	if sess == nil {
		return false
	}
	formToken := r.FormValue("csrf_token")
	if formToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(formToken), []byte(sess.CSRFToken)) == 1
}

// SecurityHeaders เพิ่ม HTTP Security Headers ตามแนวทาง OWASP A05 (Security Misconfiguration)
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com https://cdn.jsdelivr.net; script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; img-src 'self' data: https:;")
		next.ServeHTTP(w, r)
	})
}

// RequireAuth บังคับให้ผู้ใช้ต้องเข้าสู่ระบบก่อนเข้าถึงหน้าดังกล่าว
func RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := GetSession(r)
		if sess == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), UserContextKey, sess.User)
		ctx = context.WithValue(ctx, SessionContextKey, sess)
		next(w, r.WithContext(ctx))
	}
}

// RequireRole ตรวจสอบสิทธิ์ตาม Role ("admin" หรือ "customer") ป้องกัน Broken Access Control (OWASP A01)
func RequireRole(role string, next http.HandlerFunc) http.HandlerFunc {
	return RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		sess := GetSession(r)
		if sess == nil || sess.User.Role != role {
			log.Printf("[SECURITY] ปฏิเสธการเข้าถึง: ผู้ใช้ ID=%d (Role=%s) พยายามเข้าถึง %s ที่ต้องการสิทธิ์ %s",
				sess.User.ID, sess.User.Role, r.URL.Path, role)
			if sess != nil && sess.User.Role == "admin" {
				http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
				return
			}
			if sess != nil && sess.User.Role == "customer" {
				http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
				return
			}
			http.Error(w, "403 Forbidden - คุณไม่มีสิทธิ์เข้าถึงหน้านี้", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

