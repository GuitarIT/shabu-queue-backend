package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shabu-queue/config"
	"shabu-queue/middleware"
	"shabu-queue/models"
)

func init() {
	// ปรับ Working Directory ให้ชี้ไปที่ Root ของโปรเจกต์เวลาทดสอบในแพ็กเกจย่อย
	if _, err := os.Stat("templates"); os.IsNotExist(err) {
		_ = os.Chdir(filepath.Join(".."))
	}
}

func TestNormalizeTimeForSQL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		valid    bool
	}{
		{"11:00", "11:00:00", true},
		{"17:30:00", "17:30:00", true},
		{"25:00", "", false},
		{"invalid", "", false},
	}

	for _, tc := range tests {
		got, ok := normalizeTimeForSQL(tc.input)
		if ok != tc.valid || got != tc.expected {
			t.Errorf("normalizeTimeForSQL(%q) = (%q, %v), want (%q, %v)", tc.input, got, ok, tc.expected, tc.valid)
		}
	}
}

func TestSessionAndCSRFAndRBAC(t *testing.T) {
	rec := httptest.NewRecorder()
	testCustomer := models.User{
		ID:          99,
		Name:        "ลูกค้าทดสอบ",
		PhoneNumber: "0811111111",
		Role:        "customer",
	}

	sess, err := middleware.CreateSession(rec, testCustomer)
	if err != nil || sess == nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || !cookies[0].HttpOnly {
		t.Fatalf("Expected HttpOnly session cookie")
	}

	// ทดสอบการตรวจสอบ CSRF Token
	form := url.Values{}
	form.Set("csrf_token", sess.CSRFToken)
	reqValid := httptest.NewRequest(http.MethodPost, "/customer/bookings/create", strings.NewReader(form.Encode()))
	reqValid.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if !middleware.ValidateCSRF(reqValid, sess) {
		t.Errorf("Expected valid CSRF token to pass")
	}

	reqInvalid := httptest.NewRequest(http.MethodPost, "/customer/bookings/create", strings.NewReader("csrf_token=wrong"))
	reqInvalid.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if middleware.ValidateCSRF(reqInvalid, sess) {
		t.Errorf("Expected invalid CSRF token to be rejected")
	}

	// ทดสอบ RBAC: ลูกค้าต้องเข้าหน้า Admin ไม่ได้ (OWASP A01)
	adminHandler := middleware.RequireRole("admin", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	reqAdmin := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	reqAdmin.AddCookie(cookies[0])
	recAdmin := httptest.NewRecorder()
	adminHandler.ServeHTTP(recAdmin, reqAdmin)

	if recAdmin.Code != http.StatusSeeOther {
		t.Errorf("Expected customer accessing /admin/dashboard to be redirected (303), got %d", recAdmin.Code)
	}
}

func TestDatabaseAndDashboardsIntegration(t *testing.T) {
	if err := config.InitDB(); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer config.DB.Close()

	// 1. ตรวจสอบชนิดข้อมูลของคอลัมน์ booking_time และ end_time ในตาราง bookings ต้องเป็น TIME
	var timeType, endTimeType string
	err := config.DB.QueryRow(`
		SELECT DATA_TYPE
		FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = 'shabu_queue_db'
		  AND TABLE_NAME = 'bookings'
		  AND COLUMN_NAME = 'booking_time'
	`).Scan(&timeType)
	if err != nil {
		t.Fatalf("Failed to query booking_time column type: %v", err)
	}
	if strings.ToLower(timeType) != "time" {
		t.Errorf("Expected booking_time DATA_TYPE to be 'time', got %q", timeType)
	}

	err = config.DB.QueryRow(`
		SELECT DATA_TYPE
		FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = 'shabu_queue_db'
		  AND TABLE_NAME = 'bookings'
		  AND COLUMN_NAME = 'end_time'
	`).Scan(&endTimeType)
	if err == nil && strings.ToLower(endTimeType) != "time" {
		t.Errorf("Expected end_time DATA_TYPE to be 'time', got %q", endTimeType)
	}

	// 2. ทดสอบการเรนเดอร์หน้า Customer Dashboard
	recCustCookie := httptest.NewRecorder()
	_, _ = middleware.CreateSession(recCustCookie, models.User{
		ID:          2,
		Name:        "คุณสมชาย สายชาบู",
		PhoneNumber: "0899999999",
		Role:        "customer",
	})
	custCookie := recCustCookie.Result().Cookies()[0]

	reqCustDash := httptest.NewRequest(http.MethodGet, "/customer/dashboard", nil)
	reqCustDash.AddCookie(custCookie)
	recCustDash := httptest.NewRecorder()
	CustomerDashboardHandler(recCustDash, reqCustDash)
	if recCustDash.Code != http.StatusOK {
		t.Fatalf("CustomerDashboardHandler returned %d", recCustDash.Code)
	}
	if !strings.Contains(recCustDash.Body.String(), "ร้านชาบูอ้วนกลม") {
		t.Errorf("Customer dashboard body missing expected title")
	}

	// 3. ทดสอบการเรนเดอร์หน้า Admin Dashboard
	recAdminCookie := httptest.NewRecorder()
	_, _ = middleware.CreateSession(recAdminCookie, models.User{
		ID:          1,
		Name:        "เจ้าของร้านชาบูอ้วนกลม",
		PhoneNumber: "0800000001",
		Role:        "admin",
	})
	adminCookie := recAdminCookie.Result().Cookies()[0]

	reqAdminDash := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	reqAdminDash.AddCookie(adminCookie)
	recAdminDash := httptest.NewRecorder()
	AdminDashboardHandler(recAdminDash, reqAdminDash)
	if recAdminDash.Code != http.StatusOK {
		t.Fatalf("AdminDashboardHandler returned %d", recAdminDash.Code)
	}
	if !strings.Contains(recAdminDash.Body.String(), "จำนวนคิวทั้งหมดของวันนี้") {
		t.Errorf("Admin dashboard body missing expected stats")
	}
}

func TestPreventPastBookingToday(t *testing.T) {
	// 1. ตรวจสอบ templateFunc isPastTimeToday
	isPastFn, ok := templateFuncs["isPastTimeToday"].(func(string) bool)
	if !ok {
		t.Fatalf("templateFunc isPastTimeToday not found or wrong signature")
	}

	// เวลาในอดีต (00:01 น.) ต้องเป็น true เสมอ ยกเว้นทดสอบเที่ยงคืนเป๊ะ
	if !isPastFn("00:01") {
		t.Errorf("Expected 00:01 to be past time today")
	}
	// เวลาในอนาคต (23:59 น.) ต้องเป็น false
	if isPastFn("23:59") {
		t.Errorf("Expected 23:59 not to be past time today")
	}

	// 2. ทดสอบ Backend Handler ตีกลับเมื่อพยายามจองเวลาย้อนหลังสำหรับวันปัจจุบัน
	recSession := httptest.NewRecorder()
	sess, err := middleware.CreateSession(recSession, models.User{
		ID:          2,
		Name:        "ลูกค้าทดสอบ",
		PhoneNumber: "0899999999",
		Role:        "customer",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	custCookie := recSession.Result().Cookies()[0]

	now := time.Now()
	today := now.Format("2006-01-02")
	// จำลองเวลาในอดีตของวันนี้
	pastTime := "00:01"

	form := url.Values{}
	form.Set("csrf_token", sess.CSRFToken)
	form.Set("pax_count", "2")
	form.Set("booking_date", today)
	form.Set("booking_time", pastTime)

	req := httptest.NewRequest(http.MethodPost, "/customer/bookings/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(custCookie)

	rec := httptest.NewRecorder()
	CustomerCreateBookingHandler(rec, req)

	// ต้องถูก Redirect กลับไปที่ /customer/dashboard
	if rec.Code != http.StatusSeeOther {
		t.Errorf("Expected redirect 303, got %d", rec.Code)
	}

	// ตรวจสอบว่า Flash Error มีข้อความ "ไม่สามารถจองรอบเวลาย้อนหลังได้"
	_, flashErr := middleware.PopFlash(sess)
	if flashErr != "ไม่สามารถจองรอบเวลาย้อนหลังได้" {
		t.Errorf("Expected flash error 'ไม่สามารถจองรอบเวลาย้อนหลังได้', got %q", flashErr)
	}
}

