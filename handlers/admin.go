package handlers

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"shabu-queue/config"
	"shabu-queue/middleware"
	"shabu-queue/models"

	"golang.org/x/crypto/bcrypt"
)

// AdminDashboardHandler แสดงหน้า Dashboard สำหรับเจ้าของร้านและพนักงาน
func AdminDashboardHandler(w http.ResponseWriter, r *http.Request) {
	sess := middleware.GetSession(r)
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	flashSuccess, flashError := middleware.PopFlash(sess)
	today := time.Now().Format("2006-01-02")
	filterDate := strings.TrimSpace(r.URL.Query().Get("date"))
	if filterDate == "" {
		filterDate = today
	}

	settings := config.GetStoreSettings()

	// สถิติของวันนี้ (จำนวนคิวทั้งหมดของวันนี้, โต๊ะว่าง, คิวรอเรียก, คิวที่เข้าทาน)
	var totalQueuesToday, waitingToday, seatedToday int
	_ = config.DB.QueryRow(
		"SELECT COUNT(*) FROM bookings WHERE booking_date = ?",
		today,
	).Scan(&totalQueuesToday)

	_ = config.DB.QueryRow(
		"SELECT COUNT(*) FROM bookings WHERE booking_date = ? AND status = 'waiting'",
		today,
	).Scan(&waitingToday)

	// โต๊ะที่กำลังนั่งทานอยู่จริง (เฉพาะคิวที่เป็น 'seated' เท่านั้น ส่วน 'completed' คืนโต๊ะแล้ว)
	_ = config.DB.QueryRow(
		"SELECT COUNT(*) FROM bookings WHERE booking_date = ? AND status = 'seated'",
		today,
	).Scan(&seatedToday)

	availableTables := settings.TotalTables - seatedToday
	if availableTables < 0 {
		availableTables = 0
	}

	// ดึงรายการจองคิวพร้อมข้อมูลลูกค้าและ end_time
	var rows *sql.Rows
	var err error
	if filterDate == "all" {
		rows, err = config.DB.Query(`
			SELECT b.id, b.customer_id, u.name, u.phone_number, b.pax_count,
			       DATE_FORMAT(b.booking_date, '%Y-%m-%d'), TIME_FORMAT(b.booking_time, '%H:%i:%s'),
			       COALESCE(TIME_FORMAT(b.end_time, '%H:%i:%s'), ''),
			       b.status, b.created_at
			FROM bookings b
			JOIN users u ON b.customer_id = u.id
			ORDER BY b.booking_date DESC, b.booking_time ASC, b.id ASC
		`)
	} else {
		rows, err = config.DB.Query(`
			SELECT b.id, b.customer_id, u.name, u.phone_number, b.pax_count,
			       DATE_FORMAT(b.booking_date, '%Y-%m-%d'), TIME_FORMAT(b.booking_time, '%H:%i:%s'),
			       COALESCE(TIME_FORMAT(b.end_time, '%H:%i:%s'), ''),
			       b.status, b.created_at
			FROM bookings b
			JOIN users u ON b.customer_id = u.id
			WHERE b.booking_date = ?
			ORDER BY FIELD(b.status, 'waiting', 'seated', 'completed', 'cancelled'), b.booking_time ASC, b.id ASC
		`, filterDate)
	}

	var bookings []models.Booking
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var b models.Booking
			if err := rows.Scan(
				&b.ID, &b.CustomerID, &b.CustomerName, &b.CustomerPhone,
				&b.PaxCount, &b.BookingDate, &b.BookingTime, &b.EndTime, &b.Status, &b.CreatedAt,
			); err != nil {
				continue
			}
			b.QueueNumber = formatQueueCode(b.ID)
			bookings = append(bookings, b)
		}
		if err := rows.Err(); err != nil {
			log.Printf("error iterating admin bookings: %v", err)
		}
	}

	announcements, _ := fetchLatestAnnouncements(50)

	data := models.AdminDashboardData{
		CurrentUser:      sess.User,
		CSRFToken:        sess.CSRFToken,
		FlashSuccess:     flashSuccess,
		FlashError:       flashError,
		TodayDate:        today,
		FilterDate:       filterDate,
		TotalQueuesToday: totalQueuesToday,
		AvailableTables:  availableTables,
		WaitingQueues:    waitingToday,
		ServedQueues:     seatedToday,
		Settings:         settings,
		TimeSlotsJoined:  strings.Join(settings.TimeSlots, ", "),
		Bookings:         bookings,
		Announcements:    announcements,
	}

	RenderTemplate(w, "admin_dashboard.html", data)
}

// AdminUpdateBookingStatusHandler อัปเดตสถานะคิว (waiting, seated, completed, cancelled)
func AdminUpdateBookingStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	sess := middleware.GetSession(r)
	if !middleware.ValidateCSRF(r, sess) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	bookingID, err := strconv.Atoi(r.FormValue("booking_id"))
	newStatus := strings.TrimSpace(r.FormValue("status"))
	if err != nil || bookingID <= 0 || (newStatus != "waiting" && newStatus != "seated" && newStatus != "completed" && newStatus != "cancelled") {
		middleware.SetFlash(r, "", "ข้อมูลสถานะคิวไม่ถูกต้อง")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	_, err = config.DB.Exec("UPDATE bookings SET status = ? WHERE id = ?", newStatus, bookingID)
	if err != nil {
		log.Printf("[ERROR] Admin อัปเดตสถานะคิว ID=%d ล้มเหลว: %v", bookingID, err)
		middleware.SetFlash(r, "", "เกิดข้อผิดพลาดในการอัปเดตสถานะคิว")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	log.Printf("[SECURITY] Admin ID=%d เปลี่ยนสถานะคิว %s เป็น %s", sess.User.ID, formatQueueCode(bookingID), newStatus)
	var statusMsg string
	switch newStatus {
	case "seated":
		statusMsg = "เข้าทานอาหารแล้ว"
	case "completed":
		statusMsg = "ทานเสร็จเรียบร้อย (คืนโต๊ะว่างกลับสู่ระบบแล้ว)"
	case "cancelled":
		statusMsg = "ยกเลิกคิวแล้ว"
	case "waiting":
		statusMsg = "เปลี่ยนกลับเป็นรอเรียกคิว"
	default:
		statusMsg = newStatus
	}
	middleware.SetFlash(r, fmt.Sprintf("อัปเดตสถานะคิว %s เป็น '%s' เรียบร้อยแล้ว", formatQueueCode(bookingID), statusMsg), "")
	http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
}

// AdminCreateWalkInBookingHandler เพิ่มคิวใหม่โดย Admin (กรณีลูกค้า Walk-in)
func AdminCreateWalkInBookingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	sess := middleware.GetSession(r)
	if !middleware.ValidateCSRF(r, sess) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	customerName := strings.TrimSpace(r.FormValue("customer_name"))
	customerPhone := strings.TrimSpace(r.FormValue("customer_phone"))
	paxCount, err := strconv.Atoi(r.FormValue("pax_count"))
	bookingDate := strings.TrimSpace(r.FormValue("booking_date"))
	bookingTime, timeOK := normalizeTimeForSQL(r.FormValue("booking_time"))
	initialStatus := strings.TrimSpace(r.FormValue("status"))
	if initialStatus != "seated" {
		initialStatus = "waiting"
	}

	if customerName == "" || customerPhone == "" || err != nil || paxCount < 1 || paxCount > 30 || !timeOK {
		middleware.SetFlash(r, "", "กรุณากรอกชื่อ เบอร์โทรศัพท์ จำนวนคน และเวลาคิว Walk-in ให้ถูกต้อง")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	if bookingDate == "" {
		bookingDate = time.Now().Format("2006-01-02")
	}

	settings := config.GetStoreSettings()
	endTime := config.CalculateEndTime(bookingTime, settings.DiningDurationMinutes)

	// ตรวจสอบว่ามีบัญชีลูกค้าที่ใช้เบอร์โทรนี้อยู่แล้วหรือไม่
	var customerID int
	err = config.DB.QueryRow("SELECT id FROM users WHERE phone_number = ?", customerPhone).Scan(&customerID)
	if err == sql.ErrNoRows {
		defaultPassHash, _ := bcrypt.GenerateFromPassword([]byte(customerPhone), bcrypt.DefaultCost)
		resUser, errUser := config.DB.Exec(
			"INSERT INTO users (name, phone_number, password_hash, role) VALUES (?, ?, ?, 'customer')",
			customerName, customerPhone, string(defaultPassHash),
		)
		if errUser != nil {
			middleware.SetFlash(r, "", "ไม่สามารถสร้างข้อมูลลูกค้า Walk-in ได้")
			http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
			return
		}
		newCustID, _ := resUser.LastInsertId()
		customerID = int(newCustID)
	} else if err != nil {
		middleware.SetFlash(r, "", "เกิดข้อผิดพลาดในการค้นหาข้อมูลลูกค้า")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	resBooking, err := config.DB.Exec(
		"INSERT INTO bookings (customer_id, pax_count, booking_date, booking_time, end_time, status) VALUES (?, ?, ?, ?, ?, ?)",
		customerID, paxCount, bookingDate, bookingTime, endTime, initialStatus,
	)
	if err != nil {
		log.Printf("[ERROR] Admin เพิ่มคิว Walk-in ล้มเหลว: %v", err)
		middleware.SetFlash(r, "", "ไม่สามารถบันทึกคิว Walk-in ได้")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	bID, _ := resBooking.LastInsertId()
	log.Printf("[SECURITY] Admin ID=%d เพิ่มคิว Walk-in %s สำหรับ %s (%s)", sess.User.ID, formatQueueCode(int(bID)), customerName, customerPhone)
	middleware.SetFlash(r, fmt.Sprintf("เพิ่มคิว Walk-in หมายเลข %s สำหรับคุณ %s สำเร็จ (ระยะเวลาทานถึง %s)", formatQueueCode(int(bID)), customerName, templateFuncs["formatTime"].(func(string) string)(endTime)), "")
	http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
}

// AdminDeleteBookingHandler ลบคิวออกจากระบบ
func AdminDeleteBookingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	sess := middleware.GetSession(r)
	if !middleware.ValidateCSRF(r, sess) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	bookingID, err := strconv.Atoi(r.FormValue("booking_id"))
	if err != nil || bookingID <= 0 {
		middleware.SetFlash(r, "", "รหัสคิวไม่ถูกต้อง")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	_, err = config.DB.Exec("DELETE FROM bookings WHERE id = ?", bookingID)
	if err != nil {
		middleware.SetFlash(r, "", "ไม่สามารถลบคิวได้")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	log.Printf("[SECURITY] Admin ID=%d ลบคิว %s ออกจากระบบ", sess.User.ID, formatQueueCode(bookingID))
	middleware.SetFlash(r, fmt.Sprintf("ลบคิว %s ออกจากระบบเรียบร้อยแล้ว", formatQueueCode(bookingID)), "")
	http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
}

// AdminUpdateSettingsHandler ตั้งค่าระบบจองคิว (โต๊ะทั้งหมด, เวลาเปิด-ปิด, ความถี่รอบ, ระยะเวลาทาน, เปิด-ปิดรับคิว)
func AdminUpdateSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	sess := middleware.GetSession(r)
	if !middleware.ValidateCSRF(r, sess) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	totalTables, err := strconv.Atoi(r.FormValue("total_tables"))
	if err != nil || totalTables < 1 || totalTables > 500 {
		middleware.SetFlash(r, "", "จำนวนโต๊ะทั้งหมดต้องอยู่ระหว่าง 1 - 500 โต๊ะ")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	openTime := strings.TrimSpace(r.FormValue("open_time"))
	closeTime := strings.TrimSpace(r.FormValue("close_time"))
	if openTime == "" {
		openTime = "11:00"
	}
	if closeTime == "" {
		closeTime = "22:00"
	}

	slotInterval, err := strconv.Atoi(r.FormValue("slot_interval"))
	if err != nil || slotInterval < 5 || slotInterval > 120 {
		slotInterval = 15
	}

	currentSettings := config.GetStoreSettings()
	diningDuration := currentSettings.DiningDurationMinutes
	if val := strings.TrimSpace(r.FormValue("dining_duration")); val != "" {
		if d, err := strconv.Atoi(val); err == nil && d >= 15 && d <= 300 {
			diningDuration = d
		}
	}

	isOpen := r.FormValue("is_open") == "1"

	if err := config.UpdateStoreSettingsDetailed(totalTables, openTime, closeTime, slotInterval, diningDuration, isOpen); err != nil {
		log.Printf("[ERROR] บันทึกการตั้งค่าระบบจองคิวล้มเหลว: %v", err)
		middleware.SetFlash(r, "", "ไม่สามารถบันทึกการตั้งค่าระบบจองคิวได้")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	log.Printf("[SECURITY] Admin ID=%d อัปเดตตั้งค่าระบบจองคิวสำเร็จ", sess.User.ID)
	middleware.SetFlash(r, "บันทึกการตั้งค่าระบบจองคิวเรียบร้อยแล้ว", "")
	http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
}

// AdminCreateAnnouncementHandler สร้างประกาศข่าวสารใหม่
func AdminCreateAnnouncementHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	sess := middleware.GetSession(r)
	if !middleware.ValidateCSRF(r, sess) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	content := strings.TrimSpace(r.FormValue("content"))

	if title == "" || content == "" {
		middleware.SetFlash(r, "", "กรุณากรอกหัวข้อและเนื้อหาประกาศให้ครบถ้วน")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	_, err := config.DB.Exec(
		"INSERT INTO announcements (admin_id, title, content) VALUES (?, ?, ?)",
		sess.User.ID, title, content,
	)
	if err != nil {
		log.Printf("[ERROR] สร้างประกาศข่าวสารล้มเหลว: %v", err)
		middleware.SetFlash(r, "", "ไม่สามารถสร้างประกาศข่าวสารได้")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	log.Printf("[SECURITY] Admin ID=%d สร้างประกาศข่าวสารใหม่: %s", sess.User.ID, title)
	middleware.SetFlash(r, "ประกาศข่าวสารใหม่เรียบร้อยแล้ว", "")
	http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
}

// AdminEditAnnouncementHandler แก้ไขประกาศข่าวสาร
func AdminEditAnnouncementHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	sess := middleware.GetSession(r)
	if !middleware.ValidateCSRF(r, sess) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	annID, err := strconv.Atoi(r.FormValue("announcement_id"))
	title := strings.TrimSpace(r.FormValue("title"))
	content := strings.TrimSpace(r.FormValue("content"))

	if err != nil || annID <= 0 || title == "" || content == "" {
		middleware.SetFlash(r, "", "กรุณาระบุข้อมูลการแก้ไขประกาศข่าวสารให้ครบถ้วน")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	_, err = config.DB.Exec("UPDATE announcements SET title = ?, content = ? WHERE id = ?", title, content, annID)
	if err != nil {
		log.Printf("[ERROR] แก้ไขประกาศข่าวสาร ID=%d ล้มเหลว: %v", annID, err)
		middleware.SetFlash(r, "", "ไม่สามารถแก้ไขประกาศข่าวสารได้")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	log.Printf("[SECURITY] Admin ID=%d แก้ไขประกาศข่าวสาร ID=%d สำเร็จ", sess.User.ID, annID)
	middleware.SetFlash(r, "แก้ไขประกาศข่าวสารเรียบร้อยแล้ว", "")
	http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
}

// AdminDeleteAnnouncementHandler ลบประกาศข่าวสาร
func AdminDeleteAnnouncementHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	sess := middleware.GetSession(r)
	if !middleware.ValidateCSRF(r, sess) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	annID, err := strconv.Atoi(r.FormValue("announcement_id"))
	if err != nil || annID <= 0 {
		middleware.SetFlash(r, "", "รหัสประกาศไม่ถูกต้อง")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	_, err = config.DB.Exec("DELETE FROM announcements WHERE id = ?", annID)
	if err != nil {
		middleware.SetFlash(r, "", "ไม่สามารถลบประกาศข่าวสารได้")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	middleware.SetFlash(r, "ลบประกาศข่าวสารเรียบร้อยแล้ว", "")
	http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
}
