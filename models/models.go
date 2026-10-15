package models

import "time"

// User แทนตาราง users
type User struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	PhoneNumber  string `json:"phone_number"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // "admin" หรือ "customer"
}

// Announcement แทนตาราง announcements
type Announcement struct {
	ID        int       `json:"id"`
	AdminID   int       `json:"admin_id"`
	AdminName string    `json:"admin_name"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Booking แทนตาราง bookings พร้อมข้อมูลลูกค้าจากการ JOIN
type Booking struct {
	ID            int       `json:"id"`
	QueueNumber   string    `json:"queue_number"` // เช่น Q-001
	CustomerID    int       `json:"customer_id"`
	CustomerName  string    `json:"customer_name"`
	CustomerPhone string    `json:"customer_phone"`
	PaxCount      int       `json:"pax_count"`
	BookingDate   string    `json:"booking_date"` // YYYY-MM-DD
	BookingTime   string    `json:"booking_time"` // HH:MM:SS หรือ HH:MM
	EndTime       string    `json:"end_time"`     // HH:MM:SS หรือ HH:MM
	Status        string    `json:"status"`       // "waiting", "seated", "completed", "cancelled"
	QueuesAhead   int       `json:"queues_ahead"` // จำนวนคิวที่รออยู่ข้างหน้า
	CreatedAt     time.Time `json:"created_at"`
}

// StoreSettings เก็บตั้งค่ารอบคิวและจำนวนโต๊ะของร้านชาบูอ้วนกลม
type StoreSettings struct {
	TotalTables           int      `json:"total_tables"`            // จำนวนโต๊ะทั้งหมดของร้าน
	OpenTime              string   `json:"open_time"`               // เวลาเปิดรับคิว เช่น "11:00"
	CloseTime             string   `json:"close_time"`              // เวลาปิดรับคิว เช่น "22:00"
	SlotIntervalMinutes   int      `json:"slot_interval_minutes"`   // ความถี่ของรอบคิว (นาที) เช่น 15
	DiningDurationMinutes int      `json:"dining_duration_minutes"` // ระยะเวลาทานต่อรอบ (นาที) เช่น 90
	TimeSlots             []string `json:"time_slots"`              // รอบเวลาที่เปิดให้จองในรูปแบบ HH:MM
	IsOpen                bool     `json:"is_open"`                 // เปิด/ปิดรับการจองคิวออนไลน์
}

// AdminDashboardData ข้อมูลสำหรับแสดงผลหน้า Admin Dashboard
type AdminDashboardData struct {
	CurrentUser       User
	CSRFToken         string
	FlashSuccess      string
	FlashError        string
	TodayDate         string
	FilterDate        string
	TotalQueuesToday  int // จำนวนคิวทั้งหมดของวันนี้
	AvailableTables   int // จำนวนโต๊ะที่ว่าง
	WaitingQueues     int // คิวที่รอเรียก (waiting)
	ServedQueues      int // คิวที่ให้บริการเสร็จแล้ว/เข้าทานแล้ว (seated)
	Settings          StoreSettings
	TimeSlotsJoined   string
	Bookings          []Booking
	Announcements     []Announcement
	CurrentCallingNum string
}

// CustomerDashboardData ข้อมูลสำหรับแสดงผลหน้า Customer Dashboard
type CustomerDashboardData struct {
	CurrentUser       User
	CSRFToken         string
	FlashSuccess      string
	FlashError        string
	TodayDate         string
	AvailableTables   int      // จำนวนโต๊ะที่ว่าง
	TotalTables       int      // จำนวนโต๊ะทั้งหมด
	Settings          StoreSettings
	ActiveBooking     *Booking // คิวปัจจุบันของตนเอง (waiting หรือ seated วันนี้/อนาคต)
	QueuesAhead         int      // จำนวนคิวที่รออยู่ข้างหน้า
	CurrentServingLabel string   // ข้อความสถานะคิว เช่น "รอเรียก" หรือ "กำลังให้บริการ"
	CurrentServingNum   string   // คิวที่กำลังเรียก/ให้บริการอยู่ตอนนี้ของร้าน เช่น "Q-004"
	WaitingTotalToday   int      // จำนวนคิวที่กำลังรอทั้งหมดของวันนี้
	HistoryBookings   []Booking
	Announcements     []Announcement
}

