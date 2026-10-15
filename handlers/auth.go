package handlers

import (
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"shabu-queue/config"
	"shabu-queue/middleware"
	"shabu-queue/models"

	"golang.org/x/crypto/bcrypt"
)

var phoneRegex = regexp.MustCompile(`^[0-9]{9,15}$`)

var templateFuncs = template.FuncMap{
	"formatTime": func(t string) string {
		// แปลง "13:00:00" -> "13:00 น."
		if len(t) >= 5 {
			return t[:5] + " น."
		}
		return t
	},
	"rawTime": func(t string) string {
		if len(t) >= 5 {
			return t[:5]
		}
		return t
	},
	"statusText": func(status string) string {
		switch status {
		case "waiting":
			return "รอเรียกคิว"
		case "seated":
			return "เข้าทานแล้ว"
		case "completed":
			return "ทานเสร็จ"
		case "cancelled":
			return "ยกเลิกคิว"
		default:
			return status
		}
	},
	"statusBadgeClass": func(status string) string {
		switch status {
		case "waiting":
			return "badge-waiting bg-warning-subtle text-warning-emphasis border border-warning-subtle"
		case "seated":
			return "badge-seated bg-success-subtle text-success-emphasis border border-success-subtle"
		case "completed":
			return "badge-completed bg-info-subtle text-info-emphasis border border-info-subtle"
		case "cancelled":
			return "badge-cancelled bg-danger-subtle text-danger-emphasis border border-danger-subtle"
		default:
			return "bg-secondary text-white"
		}
	},
	"formatDateTime": func(t time.Time) string {
		if t.IsZero() {
			return "-"
		}
		return t.Format("02/01/2006 15:04 น.")
	},
	"isPastTimeToday": func(slot string) bool {
		now := time.Now()
		currentTime := now.Format("15:04:05")
		slotTime := strings.TrimSpace(slot)
		if len(slotTime) == 5 {
			slotTime += ":00"
		}
		return slotTime < currentTime
	},
}

// RenderTemplate เรนเดอร์ไฟล์ HTML Template พร้อมทำ Auto-Escaping ป้องกัน XSS (OWASP A03)
func RenderTemplate(w http.ResponseWriter, tmplName string, data interface{}) {
	tmplPath := filepath.Join("templates", tmplName)
	tmpl, err := template.New(tmplName).Funcs(templateFuncs).ParseFiles(tmplPath)
	if err != nil {
		log.Printf("[ERROR] ไม่สามารถโหลดเทมเพลต %s: %v", tmplName, err)
		http.Error(w, "เกิดข้อผิดพลาดภายในระบบ", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("[ERROR] ไม่สามารถเรนเดอร์เทมเพลต %s: %v", tmplName, err)
	}
}

// HomeHandler นำทางผู้ใช้ไปยังหน้า Dashboard ตามสิทธิ์การใช้งาน
func HomeHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	sess := middleware.GetSession(r)
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if sess.User.Role == "admin" {
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
}

// AuthViewData ข้อมูลสำหรับหน้า Login และ Register
type AuthViewData struct {
	Error        string
	Success      string
	PhoneNumber  string
	Name         string
	Announcements []models.Announcement
}

// LoginHandler จัดการการเข้าสู่ระบบของทั้ง Admin และ Customer
func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if sess := middleware.GetSession(r); sess != nil {
		if sess.User.Role == "admin" {
			http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		} else {
			http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		}
		return
	}

	announcements, _ := fetchLatestAnnouncements(3)

	if r.Method == http.MethodGet {
		registered := r.URL.Query().Get("registered")
		successMsg := ""
		if registered == "1" {
			successMsg = "สมัครสมาชิกสำเร็จ! กรุณาเข้าสู่ระบบ"
		}
		RenderTemplate(w, "login.html", AuthViewData{
			Success:       successMsg,
			Announcements: announcements,
		})
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	phone := strings.TrimSpace(r.FormValue("phone_number"))
	password := r.FormValue("password")

	if phone == "" || password == "" {
		RenderTemplate(w, "login.html", AuthViewData{
			Error:         "กรุณากรอกเบอร์โทรศัพท์และรหัสผ่านให้ครบถ้วน",
			PhoneNumber:   phone,
			Announcements: announcements,
		})
		return
	}

	var user models.User
	err := config.DB.QueryRow(
		"SELECT id, name, phone_number, password_hash, role FROM users WHERE phone_number = ?",
		phone,
	).Scan(&user.ID, &user.Name, &user.PhoneNumber, &user.PasswordHash, &user.Role)

	if err == sql.ErrNoRows {
		// OWASP A07 / A09: ไม่เปิดเผยว่ามีเบอร์โทรนี้ในระบบหรือไม่ และบันทึก Log
		log.Printf("[SECURITY] ความพยายามเข้าสู่ระบบล้มเหลว (ไม่พบเบอร์โทร): %s", phone)
		RenderTemplate(w, "login.html", AuthViewData{
			Error:         "เบอร์โทรศัพท์หรือรหัสผ่านไม่ถูกต้อง",
			PhoneNumber:   phone,
			Announcements: announcements,
		})
		return
	} else if err != nil {
		log.Printf("[ERROR] ค้นหาผู้ใช้ขณะ Login ล้มเหลว: %v", err)
		RenderTemplate(w, "login.html", AuthViewData{
			Error:         "เกิดข้อผิดพลาดในการเชื่อมต่อฐานข้อมูล",
			PhoneNumber:   phone,
			Announcements: announcements,
		})
		return
	}

	// OWASP A02: ตรวจสอบรหัสผ่านด้วย bcrypt
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		log.Printf("[SECURITY] ความพยายามเข้าสู่ระบบล้มเหลว (รหัสผ่านผิด) สำหรับเบอร์: %s", phone)
		RenderTemplate(w, "login.html", AuthViewData{
			Error:         "เบอร์โทรศัพท์หรือรหัสผ่านไม่ถูกต้อง",
			PhoneNumber:   phone,
			Announcements: announcements,
		})
		return
	}

	if _, err := middleware.CreateSession(w, user); err != nil {
		log.Printf("[ERROR] ไม่สามารถสร้างเซสชันได้: %v", err)
		RenderTemplate(w, "login.html", AuthViewData{
			Error:         "ไม่สามารถสร้างเซสชันเข้าสู่ระบบได้",
			PhoneNumber:   phone,
			Announcements: announcements,
		})
		return
	}

	log.Printf("[SECURITY] เข้าสู่ระบบสำเร็จ: UserID=%d Name=%s Role=%s", user.ID, user.Name, user.Role)

	if user.Role == "admin" {
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
}

// RegisterHandler จัดการการสมัครสมาชิกใหม่สำหรับลูกค้า (Customer)
func RegisterHandler(w http.ResponseWriter, r *http.Request) {
	if sess := middleware.GetSession(r); sess != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	if r.Method == http.MethodGet {
		RenderTemplate(w, "register.html", AuthViewData{})
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	phone := strings.TrimSpace(r.FormValue("phone_number"))
	password := r.FormValue("password")
	confirmPassword := r.FormValue("confirm_password")

	if name == "" || phone == "" || password == "" {
		RenderTemplate(w, "register.html", AuthViewData{
			Error:       "กรุณากรอกข้อมูลทุกช่องให้ครบถ้วน",
			Name:        name,
			PhoneNumber: phone,
		})
		return
	}

	if !phoneRegex.MatchString(phone) {
		RenderTemplate(w, "register.html", AuthViewData{
			Error:       "รูปแบบเบอร์โทรศัพท์ไม่ถูกต้อง (กรุณากรอกตัวเลข 9-15 หลัก)",
			Name:        name,
			PhoneNumber: phone,
		})
		return
	}

	if len(password) < 6 {
		RenderTemplate(w, "register.html", AuthViewData{
			Error:       "รหัสผ่านต้องมีความยาวอย่างน้อย 6 ตัวอักษร",
			Name:        name,
			PhoneNumber: phone,
		})
		return
	}

	if password != confirmPassword {
		RenderTemplate(w, "register.html", AuthViewData{
			Error:       "รหัสผ่านและการยืนยันรหัสผ่านไม่ตรงกัน",
			Name:        name,
			PhoneNumber: phone,
		})
		return
	}

	var exists int
	err := config.DB.QueryRow("SELECT COUNT(*) FROM users WHERE phone_number = ?", phone).Scan(&exists)
	if err != nil {
		log.Printf("[ERROR] ตรวจสอบเบอร์โทรศัพท์ซ้ำล้มเหลว: %v", err)
		RenderTemplate(w, "register.html", AuthViewData{
			Error:       "เกิดข้อผิดพลาดในระบบ กรุณาลองใหม่อีกครั้ง",
			Name:        name,
			PhoneNumber: phone,
		})
		return
	}
	if exists > 0 {
		RenderTemplate(w, "register.html", AuthViewData{
			Error:       "เบอร์โทรศัพท์นี้ถูกใช้งานแล้ว กรุณาเข้าสู่ระบบหรือใช้เบอร์อื่น",
			Name:        name,
			PhoneNumber: phone,
		})
		return
	}

	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		RenderTemplate(w, "register.html", AuthViewData{
			Error:       "ไม่สามารถเข้ารหัสรหัสผ่านได้",
			Name:        name,
			PhoneNumber: phone,
		})
		return
	}

	res, err := config.DB.Exec(
		"INSERT INTO users (name, phone_number, password_hash, role) VALUES (?, ?, ?, 'customer')",
		name, phone, string(hashedBytes),
	)
	if err != nil {
		log.Printf("[ERROR] บันทึกสมาชิกใหม่ล้มเหลว: %v", err)
		RenderTemplate(w, "register.html", AuthViewData{
			Error:       "ไม่สามารถสร้างบัญชีผู้ใช้ได้ กรุณาลองใหม่อีกครั้ง",
			Name:        name,
			PhoneNumber: phone,
		})
		return
	}

	newID, _ := res.LastInsertId()
	newUser := models.User{
		ID:          int(newID),
		Name:        name,
		PhoneNumber: phone,
		Role:        "customer",
	}

	_, _ = middleware.CreateSession(w, newUser)
	middleware.SetFlash(r, fmt.Sprintf("ยินดีต้อนรับคุณ %s สู่ร้านชาบูอ้วนกลม!", name), "")
	log.Printf("[SECURITY] สมัครสมาชิกใหม่สำเร็จ: UserID=%d Phone=%s", newUser.ID, newUser.PhoneNumber)

	http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
}

// LogoutHandler ออกจากระบบและทำลายเซสชัน
func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	sess := middleware.GetSession(r)
	if sess != nil {
		log.Printf("[SECURITY] ผู้ใช้ออกจากระบบ: UserID=%d Name=%s", sess.User.ID, sess.User.Name)
	}
	middleware.DestroySession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

