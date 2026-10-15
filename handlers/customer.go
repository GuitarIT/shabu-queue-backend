package handlers

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"shabu-queue/config"
	"shabu-queue/middleware"
	"shabu-queue/models"
)

var validTimeRegex = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9](:[0-5][0-9])?$`)

func formatQueueCode(id int) string {
	return fmt.Sprintf("Q-%03d", id)
}

func normalizeTimeForSQL(t string) (string, bool) {
	t = strings.TrimSpace(t)
	if !validTimeRegex.MatchString(t) {
		return "", false
	}
	if len(t) == 5 {
		return t + ":00", true
	}
	return t, true
}

func fetchLatestAnnouncements(limit int) ([]models.Announcement, error) {
	query := `
		SELECT a.id, a.admin_id, u.name, a.title, a.content, a.created_at
		FROM announcements a
		JOIN users u ON a.admin_id = u.id
		ORDER BY a.created_at DESC, a.id DESC
	`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := config.DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.Announcement
	for rows.Next() {
		var item models.Announcement
		if err := rows.Scan(&item.ID, &item.AdminID, &item.AdminName, &item.Title, &item.Content, &item.CreatedAt); err != nil {
			continue
		}
		list = append(list, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return list, nil
}

// CustomerDashboardHandler แสดงหน้า Dashboard ของลูกค้า
func CustomerDashboardHandler(w http.ResponseWriter, r *http.Request) {
	sess := middleware.GetSession(r)
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	flashSuccess, flashError := middleware.PopFlash(sess)
	today := time.Now().Format("2006-01-02")
	settings := config.GetStoreSettings()

	// 1. คำนวณจำนวนโต๊ะที่ว่าง (โต๊ะทั้งหมด - คิวที่กำลังนั่งทานวันนี้ ซึ่งมีสถานะ 'seated' เท่านั้น)
	var seatedToday int
	_ = config.DB.QueryRow(
		"SELECT COUNT(*) FROM bookings WHERE booking_date = ? AND status = 'seated'",
		today,
	).Scan(&seatedToday)

	availableTables := settings.TotalTables - seatedToday
	if availableTables < 0 {
		availableTables = 0
	}

	// 2. จำนวนคิวที่รอทั้งหมดวันนี้
	var waitingTotalToday int
	_ = config.DB.QueryRow(
		"SELECT COUNT(*) FROM bookings WHERE booking_date = ? AND status = 'waiting'",
		today,
	).Scan(&waitingTotalToday)

	// 3. ดูว่าขณะนี้ร้านกำลังเรียก/ให้บริการถึงคิวที่เท่าไหร่แล้ว
	currentServingLabel := ""
	currentServingNum := "ยังไม่มีคิวที่เรียก"
	var latestSeatedID int
	err := config.DB.QueryRow(
		"SELECT id FROM bookings WHERE booking_date = ? AND status = 'seated' ORDER BY id DESC LIMIT 1",
		today,
	).Scan(&latestSeatedID)
	if err == nil && latestSeatedID > 0 {
		currentServingLabel = "กำลังให้บริการ"
		currentServingNum = formatQueueCode(latestSeatedID)
	} else {
		var firstWaitingID int
		if errW := config.DB.QueryRow(
			"SELECT id FROM bookings WHERE booking_date = ? AND status = 'waiting' ORDER BY booking_time ASC, id ASC LIMIT 1",
			today,
		).Scan(&firstWaitingID); errW == nil && firstWaitingID > 0 {
			currentServingLabel = "รอเรียก"
			currentServingNum = formatQueueCode(firstWaitingID)
		}
	}

	// 4. ดึงประวัติการจองทั้งหมดของลูกค้าคนนี้ และหาคิวปัจจุบันที่กำลังใช้งานอยู่
	rows, err := config.DB.Query(`
		SELECT id, customer_id, pax_count, DATE_FORMAT(booking_date, '%Y-%m-%d'), TIME_FORMAT(booking_time, '%H:%i:%s'), COALESCE(TIME_FORMAT(end_time, '%H:%i:%s'), ''), status, created_at
		FROM bookings
		WHERE customer_id = ?
		ORDER BY booking_date DESC, booking_time DESC, id DESC
	`, sess.User.ID)

	var history []models.Booking
	var activeBooking *models.Booking
	queuesAhead := 0

	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var b models.Booking
			if err := rows.Scan(&b.ID, &b.CustomerID, &b.PaxCount, &b.BookingDate, &b.BookingTime, &b.EndTime, &b.Status, &b.CreatedAt); err != nil {
				continue
			}
			b.QueueNumber = formatQueueCode(b.ID)

			// ค้นหาคิวปัจจุบันของตนเอง (เน้นคิวที่ยัง waiting หรือ seated ในวันนี้/อนาคต)
			if activeBooking == nil && (b.Status == "waiting" || (b.Status == "seated" && b.BookingDate >= today)) && b.BookingDate >= today {
				bCopy := b
				activeBooking = &bCopy
			}
			history = append(history, b)
		}
		if err := rows.Err(); err != nil {
			log.Printf("error iterating customer bookings: %v", err)
		}
	}

	// 5. คำนวณจำนวนคิวที่รออยู่ข้างหน้า (สำหรับคิวที่สถานะเป็น waiting)
	if activeBooking != nil && activeBooking.Status == "waiting" {
		_ = config.DB.QueryRow(`
			SELECT COUNT(*) FROM bookings
			WHERE booking_date = ? AND status = 'waiting'
			  AND (booking_time < ? OR (booking_time = ? AND id < ?))
		`, activeBooking.BookingDate, activeBooking.BookingTime, activeBooking.BookingTime, activeBooking.ID).Scan(&queuesAhead)
		activeBooking.QueuesAhead = queuesAhead
	}

	announcements, _ := fetchLatestAnnouncements(10)

	data := models.CustomerDashboardData{
		CurrentUser:       sess.User,
		CSRFToken:         sess.CSRFToken,
		FlashSuccess:      flashSuccess,
		FlashError:        flashError,
		TodayDate:         today,
		AvailableTables:   availableTables,
		TotalTables:       settings.TotalTables,
		Settings:          settings,
		ActiveBooking:       activeBooking,
		QueuesAhead:         queuesAhead,
		CurrentServingLabel: currentServingLabel,
		CurrentServingNum:   currentServingNum,
		WaitingTotalToday:   waitingTotalToday,
		HistoryBookings:   history,
		Announcements:     announcements,
	}

	RenderTemplate(w, "customer_dashboard.html", data)
}

// CustomerCreateBookingHandler สร้างการจองคิวใหม่สำหรับลูกค้า
func CustomerCreateBookingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	sess := middleware.GetSession(r)
	if !middleware.ValidateCSRF(r, sess) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	settings := config.GetStoreSettings()
	if !settings.IsOpen {
		middleware.SetFlash(r, "", "ขออภัย ขณะนี้ทางร้านปิดรับการจองคิวชั่วคราว")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	paxCount, err := strconv.Atoi(r.FormValue("pax_count"))
	if err != nil || paxCount < 1 || paxCount > 20 {
		middleware.SetFlash(r, "", "จำนวนลูกค้าต้องอยู่ระหว่าง 1 - 20 ท่าน")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	bookingDate := strings.TrimSpace(r.FormValue("booking_date"))
	today := time.Now().Format("2006-01-02")
	if _, err := time.Parse("2006-01-02", bookingDate); err != nil || bookingDate < today {
		middleware.SetFlash(r, "", "วันที่จองไม่ถูกต้อง หรือไม่สามารถจองย้อนหลังได้")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	bookingTime, ok := normalizeTimeForSQL(r.FormValue("booking_time"))
	if !ok {
		middleware.SetFlash(r, "", "กรุณาเลือกรอบเวลาที่เปิดให้บริการ")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	// ป้องกันการจองเวลาย้อนหลังสำหรับวันปัจจุบัน (Backend Validation ก่อนคำสั่ง INSERT)
	currentTime := time.Now().Format("15:04:05")
	if bookingDate == today && bookingTime < currentTime {
		middleware.SetFlash(r, "", "ไม่สามารถจองรอบเวลาย้อนหลังได้")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	// ตรวจสอบว่าลูกค้ามีคิวที่กำลังรอ (waiting) ในวันเดียวกันอยู่แล้วหรือไม่
	var existingWaiting int
	_ = config.DB.QueryRow(
		"SELECT COUNT(*) FROM bookings WHERE customer_id = ? AND booking_date = ? AND status = 'waiting'",
		sess.User.ID, bookingDate,
	).Scan(&existingWaiting)
	if existingWaiting > 0 {
		middleware.SetFlash(r, "", "คุณมีคิวที่กำลังรอเรียกในวันที่เลือกอยู่แล้ว สามารถแก้ไขจำนวนคนหรือเลื่อนเวลาได้ที่คิวปัจจุบันของคุณ")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	// คำนวณ end_time อัตโนมัติจากระยะเวลาทานต่อรอบ
	endTime := config.CalculateEndTime(bookingTime, settings.DiningDurationMinutes)

	// ตรวจสอบ Overbooking: ตรวจสอบจำนวนโต๊ะที่ซ้อนทับกัน (Time Overlapping) ในช่วง [booking_time, end_time]
	var overlappingCount int
	_ = config.DB.QueryRow(`
		SELECT COUNT(*) FROM bookings
		WHERE booking_date = ?
		  AND status = 'seated'
		  AND booking_time < ? AND end_time > ?
	`, bookingDate, endTime, bookingTime).Scan(&overlappingCount)

	if overlappingCount >= settings.TotalTables {
		middleware.SetFlash(r, "", fmt.Sprintf("ขออภัย รอบเวลา %s น. มีโต๊ะนั่งเต็มจำนวน %d โต๊ะแล้ว กรุณาเลือกรอบเวลาอื่น", templateFuncs["rawTime"].(func(string) string)(bookingTime), settings.TotalTables))
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	res, err := config.DB.Exec(
		"INSERT INTO bookings (customer_id, pax_count, booking_date, booking_time, end_time, status) VALUES (?, ?, ?, ?, ?, 'waiting')",
		sess.User.ID, paxCount, bookingDate, bookingTime, endTime,
	)
	if err != nil {
		log.Printf("[ERROR] สร้างคิวใหม่ล้มเหลว: %v", err)
		middleware.SetFlash(r, "", "เกิดข้อผิดพลาดในการบันทึกการจองคิว")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	bookingID, _ := res.LastInsertId()
	qCode := formatQueueCode(int(bookingID))
	log.Printf("[INFO] ลูกค้า ID=%d จองคิวสำเร็จ: %s (%d ท่าน วันที่ %s เวลา %s - %s)", sess.User.ID, qCode, paxCount, bookingDate, bookingTime, endTime)
	middleware.SetFlash(r, fmt.Sprintf("จองคิวสำเร็จ! หมายเลขคิวของคุณคือ %s (รอบเวลา %s - %s)", qCode, templateFuncs["formatTime"].(func(string) string)(bookingTime), templateFuncs["formatTime"].(func(string) string)(endTime)), "")
	http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
}

// CustomerEditBookingHandler แก้ไขจำนวนคนหรือเลื่อนเวลาจองคิวของตนเอง
func CustomerEditBookingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	sess := middleware.GetSession(r)
	if !middleware.ValidateCSRF(r, sess) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	bookingID, err := strconv.Atoi(r.FormValue("booking_id"))
	if err != nil || bookingID <= 0 {
		middleware.SetFlash(r, "", "รหัสการจองคิวไม่ถูกต้อง")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	paxCount, err := strconv.Atoi(r.FormValue("pax_count"))
	if err != nil || paxCount < 1 || paxCount > 20 {
		middleware.SetFlash(r, "", "จำนวนลูกค้าต้องอยู่ระหว่าง 1 - 20 ท่าน")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	bookingDate := strings.TrimSpace(r.FormValue("booking_date"))
	today := time.Now().Format("2006-01-02")
	if _, err := time.Parse("2006-01-02", bookingDate); err != nil || bookingDate < today {
		middleware.SetFlash(r, "", "วันที่จองไม่ถูกต้อง หรือไม่สามารถเลื่อนไปวันย้อนหลังได้")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	bookingTime, ok := normalizeTimeForSQL(r.FormValue("booking_time"))
	if !ok {
		middleware.SetFlash(r, "", "รูปแบบเวลาจองไม่ถูกต้อง")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	// ป้องกันการแก้ไขเวลาจองย้อนหลังสำหรับวันปัจจุบัน
	currentTime := time.Now().Format("15:04:05")
	if bookingDate == today && bookingTime < currentTime {
		middleware.SetFlash(r, "", "ไม่สามารถจองรอบเวลาย้อนหลังได้")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	settings := config.GetStoreSettings()
	endTime := config.CalculateEndTime(bookingTime, settings.DiningDurationMinutes)

	// OWASP A01: บังคับตรวจสอบ customer_id = sess.User.ID และ status = 'waiting'
	res, err := config.DB.Exec(
		"UPDATE bookings SET pax_count = ?, booking_date = ?, booking_time = ?, end_time = ? WHERE id = ? AND customer_id = ? AND status = 'waiting'",
		paxCount, bookingDate, bookingTime, endTime, bookingID, sess.User.ID,
	)
	if err != nil {
		log.Printf("[ERROR] แก้ไขคิว ID=%d ล้มเหลว: %v", bookingID, err)
		middleware.SetFlash(r, "", "ไม่สามารถแก้ไขข้อมูลการจองได้")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		log.Printf("[SECURITY] ปฏิเสธการแก้ไขคิว ID=%d โดย UserID=%d", bookingID, sess.User.ID)
		middleware.SetFlash(r, "", "ไม่สามารถแก้ไขคิวนี้ได้ (แก้ไขได้เฉพาะคิวของคุณที่ยังอยู่ในสถานะรอเรียกเท่านั้น)")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	middleware.SetFlash(r, fmt.Sprintf("อัปเดตข้อมูลคิว %s เรียบร้อยแล้ว (เวลา %s - %s)", formatQueueCode(bookingID), templateFuncs["formatTime"].(func(string) string)(bookingTime), templateFuncs["formatTime"].(func(string) string)(endTime)), "")
	http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
}

// CustomerCancelBookingHandler ยกเลิกคิวของตนเอง
func CustomerCancelBookingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
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
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	// OWASP A01: บังคับตรวจสอบความเป็นเจ้าของคิว (customer_id = sess.User.ID)
	res, err := config.DB.Exec(
		"UPDATE bookings SET status = 'cancelled' WHERE id = ? AND customer_id = ? AND status = 'waiting'",
		bookingID, sess.User.ID,
	)
	if err != nil {
		middleware.SetFlash(r, "", "เกิดข้อผิดพลาดในการยกเลิกคิว")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		middleware.SetFlash(r, "", "ไม่สามารถยกเลิกคิวนี้ได้")
		http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
		return
	}

	log.Printf("[INFO] ลูกค้า ID=%d ยกเลิกคิว ID=%d สำเร็จ", sess.User.ID, bookingID)
	middleware.SetFlash(r, fmt.Sprintf("ยกเลิกคิว %s เรียบร้อยแล้ว", formatQueueCode(bookingID)), "")
	http.Redirect(w, r, "/customer/dashboard", http.StatusSeeOther)
}

var _ = sql.ErrNoRows
