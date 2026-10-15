package config

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"shabu-queue/models"

	_ "github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

var (
	DB           *sql.DB
	settingsMu   sync.RWMutex
	settingsPath = filepath.Join("config", "store_settings.json")
	currentStore = models.StoreSettings{
		TotalTables:           20,
		OpenTime:              "11:00",
		CloseTime:             "22:00",
		SlotIntervalMinutes:   15,
		DiningDurationMinutes: 90,
		TimeSlots:             GenerateTimeSlots("11:00", "22:00", 15),
		IsOpen:                true,
	}
)

// GenerateTimeSlots สร้างรอบเวลาจองตั้งแต่ openTime ถึง closeTime โดยห่างกันรอบละ intervalMinutes นาที
func GenerateTimeSlots(openTime, closeTime string, intervalMinutes int) []string {
	if intervalMinutes <= 0 {
		intervalMinutes = 15
	}
	if openTime == "" {
		openTime = "11:00"
	}
	if closeTime == "" {
		closeTime = "22:00"
	}
	tOpen, err1 := time.Parse("15:04", openTime)
	tClose, err2 := time.Parse("15:04", closeTime)
	if err1 != nil || err2 != nil || !tOpen.Before(tClose) {
		return []string{"11:00", "11:30", "12:00", "12:30", "13:00", "17:00", "17:30", "18:00", "18:30", "19:00"}
	}
	var slots []string
	curr := tOpen
	for !curr.After(tClose) {
		slots = append(slots, curr.Format("15:04"))
		curr = curr.Add(time.Duration(intervalMinutes) * time.Minute)
	}
	return slots
}

// CalculateEndTime คำนวณเวลาสิ้นสุดการทาน (end_time) จาก booking_time + durationMinutes
func CalculateEndTime(bookingTime string, durationMinutes int) string {
	if durationMinutes <= 0 {
		durationMinutes = 90
	}
	trimmed := bookingTime
	if len(trimmed) > 5 {
		trimmed = trimmed[:5]
	}
	t, err := time.Parse("15:04", trimmed)
	if err != nil {
		return "12:30:00"
	}
	endTime := t.Add(time.Duration(durationMinutes) * time.Minute)
	return endTime.Format("15:04:00")
}

// InitDB เชื่อมต่อ MySQL บนพอร์ต 3306 เท่านั้น และสร้างฐานข้อมูล/ตาราง/ข้อมูลเริ่มต้นอัตโนมัติ
func InitDB() error {
	dbUser := getEnv("DB_USER", "root")
	dbPass := os.Getenv("DB_PASS")
	dbHost := getEnv("DB_HOST", "127.0.0.1")
	// บังคับให้รันได้เฉพาะพอร์ต 3306 อย่างเดียวตามความต้องการของผู้ใช้งาน
	dbPort := "3306"
	dbName := getEnv("DB_NAME", "shabu_queue_db")

	candidatePasswords := []string{dbPass}
	if dbPass == "" {
		candidatePasswords = []string{"", "root", "1234", "123456"}
	}

	var rootDB *sql.DB
	var connectedPass string
	var lastPingErr error

	for _, pass := range candidatePasswords {
		rootDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/?charset=utf8mb4&parseTime=True&loc=Local", dbUser, pass, dbHost, dbPort)
		dbConn, err := sql.Open("mysql", rootDSN)
		if err != nil {
			lastPingErr = err
			continue
		}
		if err := dbConn.Ping(); err == nil {
			rootDB = dbConn
			connectedPass = pass
			lastPingErr = nil
			break
		} else {
			lastPingErr = err
			_ = dbConn.Close()
		}
	}

	if rootDB == nil {
		return fmt.Errorf("ไม่สามารถเชื่อมต่อ MySQL พอร์ต 3306 ที่ %s:%s (กรุณาตรวจสอบว่าเปิด MySQL บนพอร์ต 3306 ใน XAMPP Control Panel แล้ว): %w", dbHost, dbPort, lastPingErr)
	}
	defer rootDB.Close()

	_, err := rootDB.Exec(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;", dbName))
	if err != nil {
		return fmt.Errorf("ไม่สามารถสร้างฐานข้อมูล %s: %w", dbName, err)
	}

	// 2. เชื่อมต่อเข้าฐานข้อมูล shabu_queue_db บนพอร์ต 3306
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local", dbUser, connectedPass, dbHost, dbPort, dbName)
	DB, err = sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("ไม่สามารถเชื่อมต่อฐานข้อมูล %s: %w", dbName, err)
	}

	DB.SetMaxOpenConns(25)
	DB.SetMaxIdleConns(10)
	DB.SetConnMaxLifetime(5 * time.Minute)

	if err := DB.Ping(); err != nil {
		return err
	}

	if err := migrateTables(); err != nil {
		return err
	}

	LoadStoreSettings()

	if err := seedInitialData(); err != nil {
		log.Printf("[WARN] การสร้างข้อมูลเริ่มต้นมีข้อผิดพลาด: %v", err)
	}

	log.Printf("[INFO] เชื่อมต่อฐานข้อมูล MySQL พอร์ต 3306 (%s) สำเร็จเรียบร้อย", dbName)
	return nil
}

func migrateTables() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			phone_number VARCHAR(20) NOT NULL UNIQUE,
			password_hash VARCHAR(255) NOT NULL,
			role ENUM('admin', 'customer') NOT NULL DEFAULT 'customer'
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		`CREATE TABLE IF NOT EXISTS announcements (
			id INT AUTO_INCREMENT PRIMARY KEY,
			admin_id INT NOT NULL,
			title VARCHAR(200) NOT NULL,
			content TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (admin_id) REFERENCES users(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		`CREATE TABLE IF NOT EXISTS bookings (
			id INT AUTO_INCREMENT PRIMARY KEY,
			customer_id INT NOT NULL,
			pax_count INT NOT NULL,
			booking_date DATE NOT NULL,
			booking_time TIME NOT NULL,
			end_time TIME NOT NULL,
			status ENUM('waiting', 'seated', 'completed', 'cancelled') NOT NULL DEFAULT 'waiting',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (customer_id) REFERENCES users(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	}

	for _, q := range queries {
		if _, err := DB.Exec(q); err != nil {
			return fmt.Errorf("เกิดข้อผิดพลาดในการสร้างตาราง: %w", err)
		}
	}

	// รองรับกรณีตาราง bookings มีอยู่แล้วแต่ยังไม่มีคอลัมน์ end_time หรือยังไม่มี ENUM completed
	_, _ = DB.Exec("ALTER TABLE bookings ADD COLUMN end_time TIME NULL AFTER booking_time;")
	_, _ = DB.Exec("UPDATE bookings SET end_time = ADDTIME(booking_time, '01:30:00') WHERE end_time IS NULL;")
	_, _ = DB.Exec("ALTER TABLE bookings MODIFY COLUMN status ENUM('waiting', 'seated', 'completed', 'cancelled') NOT NULL DEFAULT 'waiting';")

	return nil
}

func seedInitialData() error {
	var userCount int
	if err := DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount); err != nil {
		return err
	}
	if userCount > 0 {
		return nil
	}

	adminHash, err := bcrypt.GenerateFromPassword([]byte("admin1234"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	custHash, err := bcrypt.GenerateFromPassword([]byte("customer1234"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	resAdmin, err := DB.Exec(
		"INSERT INTO users (name, phone_number, password_hash, role) VALUES (?, ?, ?, 'admin')",
		"เจ้าของร้านชาบูอ้วนกลม", "0800000001", string(adminHash),
	)
	if err != nil {
		return err
	}
	adminID, _ := resAdmin.LastInsertId()

	resCust1, err := DB.Exec(
		"INSERT INTO users (name, phone_number, password_hash, role) VALUES (?, ?, ?, 'customer')",
		"คุณสมชาย สายชาบู", "0899999999", string(custHash),
	)
	if err != nil {
		return err
	}
	cust1ID, _ := resCust1.LastInsertId()

	resCust2, err := DB.Exec(
		"INSERT INTO users (name, phone_number, password_hash, role) VALUES (?, ?, ?, 'customer')",
		"คุณพิมพ์ใจ ชอบเนื้อวากิว", "0812345678", string(custHash),
	)
	if err != nil {
		return err
	}
	cust2ID, _ := resCust2.LastInsertId()

	// สร้างประกาศข่าวสารตัวอย่าง
	_, _ = DB.Exec(
		"INSERT INTO announcements (admin_id, title, content) VALUES (?, ?, ?)",
		adminID,
		"🔥 โปรโมชั่นพิเศษ! มา 4 จ่าย 3 ทุกวันจันทร์-ศุกร์",
		"อิ่มไม่อั้นกับเนื้อวากิว หมูคุโรบูตะ และกุ้งแม่น้ำสดๆ เพียงจองคิวล่วงหน้าผ่านระบบเว็บไซต์ รับฟรีชีสยืด 1 ถาดต่อโต๊ะ!",
	)
	_, _ = DB.Exec(
		"INSERT INTO announcements (admin_id, title, content) VALUES (?, ?, ?)",
		adminID,
		"🍲 น้ำซุปใหม่! ซุปหมาล่าเข้มข้น & ซุปทงคตสึกระดูกหมู",
		"เปิดให้ลิ้มลองแล้ววันนี้ที่ร้านชาบูอ้วนกลมทุกสาขา เลือกได้สูงสุด 2 ช่องน้ำซุปโดยไม่เสียค่าใช้จ่ายเพิ่ม",
	)

	// สร้างคิวตัวอย่างสำหรับวันนี้
	today := time.Now().Format("2006-01-02")
	_, _ = DB.Exec(
		"INSERT INTO bookings (customer_id, pax_count, booking_date, booking_time, end_time, status) VALUES (?, ?, ?, ?, ?, 'seated')",
		cust2ID, 4, today, "11:00:00", "12:30:00",
	)
	_, _ = DB.Exec(
		"INSERT INTO bookings (customer_id, pax_count, booking_date, booking_time, end_time, status) VALUES (?, ?, ?, ?, ?, 'waiting')",
		cust1ID, 2, today, "13:00:00", "14:30:00",
	)

	log.Println("[INFO] สร้างข้อมูลตัวอย่างเริ่มต้นเรียบร้อยแล้ว")
	return nil
}

// LoadStoreSettings โหลดค่าตั้งค่ารอบคิวและจำนวนโต๊ะจากไฟล์ JSON
func LoadStoreSettings() {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		_ = saveStoreSettingsUnlocked()
		return
	}
	var loaded models.StoreSettings
	if err := json.Unmarshal(data, &loaded); err == nil && loaded.TotalTables > 0 {
		if loaded.OpenTime == "" {
			loaded.OpenTime = "11:00"
		}
		if loaded.CloseTime == "" {
			loaded.CloseTime = "22:00"
		}
		if loaded.SlotIntervalMinutes <= 0 {
			loaded.SlotIntervalMinutes = 15
		}
		if loaded.DiningDurationMinutes <= 0 {
			loaded.DiningDurationMinutes = 90
		}
		if len(loaded.TimeSlots) == 0 {
			loaded.TimeSlots = GenerateTimeSlots(loaded.OpenTime, loaded.CloseTime, loaded.SlotIntervalMinutes)
		}
		currentStore = loaded
	}
}

// GetStoreSettings คืนค่าการตั้งค่าร้านปัจจุบันอย่างปลอดภัย (Thread-safe)
func GetStoreSettings() models.StoreSettings {
	settingsMu.RLock()
	defer settingsMu.RUnlock()

	slotsCopy := make([]string, len(currentStore.TimeSlots))
	copy(slotsCopy, currentStore.TimeSlots)
	return models.StoreSettings{
		TotalTables:           currentStore.TotalTables,
		OpenTime:              currentStore.OpenTime,
		CloseTime:             currentStore.CloseTime,
		SlotIntervalMinutes:   currentStore.SlotIntervalMinutes,
		DiningDurationMinutes: currentStore.DiningDurationMinutes,
		TimeSlots:             slotsCopy,
		IsOpen:                currentStore.IsOpen,
	}
}

// UpdateStoreSettingsDetailed บันทึกการตั้งค่าร้านตามเมนูที่กำหนด
func UpdateStoreSettingsDetailed(totalTables int, openTime, closeTime string, slotInterval, diningDuration int, isOpen bool) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	if totalTables < 1 {
		totalTables = 1
	}
	if slotInterval < 5 {
		slotInterval = 10
	}
	if diningDuration < 15 {
		diningDuration = 90
	}
	if openTime == "" {
		openTime = "11:00"
	}
	if closeTime == "" {
		closeTime = "22:00"
	}

	slots := GenerateTimeSlots(openTime, closeTime, slotInterval)

	currentStore.TotalTables = totalTables
	currentStore.OpenTime = openTime
	currentStore.CloseTime = closeTime
	currentStore.SlotIntervalMinutes = slotInterval
	currentStore.DiningDurationMinutes = diningDuration
	currentStore.TimeSlots = slots
	currentStore.IsOpen = isOpen

	return saveStoreSettingsUnlocked()
}

func saveStoreSettingsUnlocked() error {
	_ = os.MkdirAll(filepath.Dir(settingsPath), 0755)
	data, err := json.MarshalIndent(currentStore, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath, data, 0644)
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
