package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"shabu-queue/config"
	"shabu-queue/handlers"
	"shabu-queue/middleware"
)

func main() {
	if err := config.InitDB(); err != nil {
		log.Fatalf("[FATAL] เริ่มต้นฐานข้อมูลไม่สำเร็จ: %v", err)
	}
	defer config.DB.Close()

	mux := http.NewServeMux()

	// เสิร์ฟไฟล์ Static (CSS, JS)
	fs := http.FileServer(http.Dir("static"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	// Public & Auth Routes
	mux.HandleFunc("/", handlers.HomeHandler)
	mux.HandleFunc("/login", handlers.LoginHandler)
	mux.HandleFunc("/register", handlers.RegisterHandler)
	mux.HandleFunc("/logout", handlers.LogoutHandler)

	// Customer Routes (RBAC: เฉพาะ role = 'customer')
	mux.HandleFunc("/customer/dashboard", middleware.RequireRole("customer", handlers.CustomerDashboardHandler))
	mux.HandleFunc("/customer/bookings/create", middleware.RequireRole("customer", handlers.CustomerCreateBookingHandler))
	mux.HandleFunc("/customer/bookings/edit", middleware.RequireRole("customer", handlers.CustomerEditBookingHandler))
	mux.HandleFunc("/customer/bookings/cancel", middleware.RequireRole("customer", handlers.CustomerCancelBookingHandler))

	// Admin Routes (RBAC: เฉพาะ role = 'admin')
	mux.HandleFunc("/admin/dashboard", middleware.RequireRole("admin", handlers.AdminDashboardHandler))
	mux.HandleFunc("/admin/bookings/status", middleware.RequireRole("admin", handlers.AdminUpdateBookingStatusHandler))
	mux.HandleFunc("/admin/bookings/create", middleware.RequireRole("admin", handlers.AdminCreateWalkInBookingHandler))
	mux.HandleFunc("/admin/bookings/delete", middleware.RequireRole("admin", handlers.AdminDeleteBookingHandler))
	mux.HandleFunc("/admin/settings/update", middleware.RequireRole("admin", handlers.AdminUpdateSettingsHandler))
	mux.HandleFunc("/admin/announcements/create", middleware.RequireRole("admin", handlers.AdminCreateAnnouncementHandler))
	mux.HandleFunc("/admin/announcements/edit", middleware.RequireRole("admin", handlers.AdminEditAnnouncementHandler))
	mux.HandleFunc("/admin/announcements/delete", middleware.RequireRole("admin", handlers.AdminDeleteAnnouncementHandler))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      middleware.SecurityHeaders(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	fmt.Println("============================================================")
	fmt.Println("🍲 ระบบจองคิวร้านชาบูอ้วนกลม (Ouan-Glom Shabu Queue System)")
	fmt.Printf("🚀 เซิร์ฟเวอร์ทำงานที่: http://localhost:%s\n", port)
	fmt.Println("🔑 บัญชี Admin:    0800000001 / admin1234")
	fmt.Println("🔑 บัญชี Customer: 0899999999 / customer1234")
	fmt.Println("============================================================")

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[FATAL] เซิร์ฟเวอร์หยุดทำงาน: %v", err)
	}
}

