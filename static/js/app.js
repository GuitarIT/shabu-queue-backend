document.addEventListener("DOMContentLoaded", function () {
    // 1. แจ้งเตือน Flash Popup: แสดงประมาณ 5 วินาทีแล้วปิดอัตโนมัติ (แต่ยังคงมีกากบาท X ให้กดปิดได้)
    const alertElements = document.querySelectorAll(".alert");
    alertElements.forEach(function (alertEl) {
        setTimeout(function () {
            alertEl.classList.add("fade-out");
            setTimeout(function () {
                try {
                    const bsAlert = bootstrap.Alert.getOrCreateInstance(alertEl);
                    bsAlert.close();
                } catch (e) {
                    alertEl.remove();
                }
            }, 500);
        }, 5000); // 5 วินาที
    });

    // 2. ป้องกันการจองเวลาย้อนหลังสำหรับวันปัจจุบัน และระบบเลือกรอบเวลา (Slot Buttons & Dropdown)
    function setupTimeSlotProtection(dateInputId, containerId, selectId) {
        const dateInput = document.getElementById(dateInputId);
        if (!dateInput) return;

        function refreshSlots() {
            const selectedDate = dateInput.value;
            const now = new Date();
            const todayStr = now.getFullYear() + "-" +
                String(now.getMonth() + 1).padStart(2, '0') + "-" +
                String(now.getDate()).padStart(2, '0');
            const isToday = (selectedDate === todayStr);

            const currentHour = String(now.getHours()).padStart(2, '0');
            const currentMin = String(now.getMinutes()).padStart(2, '0');
            const currentTimeStr = currentHour + ":" + currentMin;

            // 1) จัดการปุ่มตัวเลือกรอบเวลา (Slot Buttons)
            if (containerId) {
                const slotContainer = document.getElementById(containerId);
                if (slotContainer) {
                    const buttons = slotContainer.querySelectorAll(".js-slot-btn");
                    buttons.forEach(function (btn) {
                        const slotTime = btn.getAttribute("data-time");
                        const isPast = isToday && (slotTime <= currentTimeStr);
                        if (isPast) {
                            btn.disabled = true;
                            btn.setAttribute("disabled", "disabled");
                            btn.classList.add("disabled");
                            if (btn.classList.contains("active")) {
                                btn.classList.remove("active");
                            }
                        } else {
                            btn.disabled = false;
                            btn.removeAttribute("disabled");
                            btn.classList.remove("disabled");
                        }
                    });
                }
            }

            // 2) จัดการตัวเลือกใน Dropdown (Select Options)
            if (selectId) {
                const selectEl = document.getElementById(selectId);
                if (selectEl) {
                    let isCurrentValueDisabled = false;
                    Array.from(selectEl.options).forEach(function (opt) {
                        if (!opt.value) return; // ข้ามตัวเลือก placeholder
                        const slotTime = opt.value;
                        const isPast = isToday && (slotTime <= currentTimeStr);
                        if (isPast) {
                            opt.disabled = true;
                            opt.textContent = slotTime + " น. (เลยเวลาแล้ว)";
                            if (selectEl.value === slotTime) {
                                isCurrentValueDisabled = true;
                            }
                        } else {
                            opt.disabled = false;
                            opt.textContent = slotTime + " น.";
                        }
                    });

                    // ถ้ารอบเวลาที่เลือกไว้ปัจจุบันเลยเวลาแล้ว ให้รีเซ็ตไปตัวเลือกที่ยังไม่หมดเวลา
                    if (isCurrentValueDisabled) {
                        const firstValid = Array.from(selectEl.options).find(o => !o.disabled && o.value);
                        selectEl.value = firstValid ? firstValid.value : "";
                        if (selectEl.value) {
                            selectEl.dispatchEvent(new Event("change"));
                        }
                    }
                }
            }
        }

        dateInput.addEventListener("change", refreshSlots);
        dateInput.addEventListener("input", refreshSlots);
        refreshSlots();
    }

    // เรียกใช้งานทั้งฟอร์มจองหลัก และ Modal แก้ไขการจอง
    setupTimeSlotProtection("booking_date", null, "booking_time");
    setupTimeSlotProtection("edit_booking_date", null, "edit_booking_time");

    // คลิกปุ่มเลือกรอบเวลา (Slot Buttons)
    const slotBtns = document.querySelectorAll(".js-slot-btn");
    slotBtns.forEach(function (btn) {
        btn.addEventListener("click", function () {
            if (btn.disabled || btn.classList.contains("disabled")) {
                return;
            }
            const timeVal = btn.getAttribute("data-time");
            const targetInputId = btn.getAttribute("data-target") || "booking_time";
            const targetInput = document.getElementById(targetInputId);
            if (targetInput) {
                targetInput.value = timeVal;
                targetInput.dispatchEvent(new Event("change"));
            }
            const container = btn.closest(".slot-grid") || btn.parentElement;
            if (container) {
                container.querySelectorAll(".js-slot-btn").forEach(function (b) {
                    b.classList.remove("active");
                });
            }
            btn.classList.add("active");
        });
    });

    // เมื่อเปลี่ยนค่าใน Dropdown booking_time ให้ไฮไลต์ปุ่ม Slot Button ที่ตรงกัน
    const mainTimeSelect = document.getElementById("booking_time");
    if (mainTimeSelect) {
        mainTimeSelect.addEventListener("change", function () {
            const val = this.value;
            const container = document.getElementById("booking_slot_buttons");
            if (container) {
                container.querySelectorAll(".js-slot-btn").forEach(function (b) {
                    if (b.getAttribute("data-time") === val && !b.disabled) {
                        b.classList.add("active");
                    } else {
                        b.classList.remove("active");
                    }
                });
            }
        });
    }

    // 3. ระบบแจ้งเตือนคิวล่าช้าเกิน 5 นาที สำหรับ Admin Dashboard
    function checkLateQueues() {
        const rows = document.querySelectorAll("tr[data-booking-status='waiting']");
        if (!rows.length) return;

        const now = new Date();
        const todayStr = now.getFullYear() + "-" +
            String(now.getMonth() + 1).padStart(2, '0') + "-" +
            String(now.getDate()).padStart(2, '0');

        rows.forEach(function (row) {
            const bDate = row.getAttribute("data-booking-date");
            const bTime = row.getAttribute("data-booking-time");
            if (bDate !== todayStr || !bTime) return;

            const timeParts = bTime.split(":");
            if (timeParts.length < 2) return;

            const bookingDateTime = new Date(
                now.getFullYear(),
                now.getMonth(),
                now.getDate(),
                parseInt(timeParts[0], 10),
                parseInt(timeParts[1], 10),
                0
            );

            // คำนวณส่วนต่างเป็นนาที
            const diffMs = now - bookingDateTime;
            const diffMinutes = Math.floor(diffMs / 60000);

            const warnContainer = row.querySelector(".js-late-warning-container");
            if (diffMinutes >= 5) {
                row.classList.add("queue-row-late");
                if (warnContainer && !warnContainer.querySelector(".badge-late-warning")) {
                    warnContainer.innerHTML = `
                        <div class="badge-late-warning mt-1 text-nowrap" style="white-space: nowrap;">
                            <i class="bi bi-exclamation-triangle-fill"></i> มาช้ากว่าเวลาจอง ${diffMinutes} นาที
                        </div>
                    `;
                }
            } else {
                row.classList.remove("queue-row-late");
                if (warnContainer) {
                    warnContainer.innerHTML = "";
                }
            }
        });
    }

    checkLateQueues();
    // ตรวจสอบความล่าช้าซ้ำทุก 30 วินาที
    setInterval(checkLateQueues, 30000);

    // 4. Modal แก้ไขประกาศข่าวสารของ Admin
    const editAnnButtons = document.querySelectorAll(".js-edit-ann-btn");
    editAnnButtons.forEach(function (btn) {
        btn.addEventListener("click", function () {
            const id = btn.getAttribute("data-id");
            const title = btn.getAttribute("data-title");
            const content = btn.getAttribute("data-content");

            const idEl = document.getElementById("edit_announcement_id");
            const titleEl = document.getElementById("edit_announcement_title");
            const contentEl = document.getElementById("edit_announcement_content");

            if (idEl) idEl.value = id;
            if (titleEl) titleEl.value = title;
            if (contentEl) contentEl.value = content;
        });
    });

    // 5. Modal แก้ไขคิวของลูกค้า
    const editBookingButtons = document.querySelectorAll(".js-edit-booking-btn");
    editBookingButtons.forEach(function (btn) {
        btn.addEventListener("click", function () {
            const id = btn.getAttribute("data-id");
            const qnum = btn.getAttribute("data-qnum");
            const pax = btn.getAttribute("data-pax");
            const date = btn.getAttribute("data-date");
            const time = btn.getAttribute("data-time");

            const idEl = document.getElementById("edit_booking_id");
            const titleEl = document.getElementById("edit_booking_qnum");
            const paxEl = document.getElementById("edit_pax_count");
            const dateEl = document.getElementById("edit_booking_date");
            const timeEl = document.getElementById("edit_booking_time");

            if (idEl) idEl.value = id;
            if (titleEl) titleEl.textContent = qnum;
            if (paxEl) paxEl.value = pax;
            if (dateEl) {
                dateEl.value = date;
                dateEl.dispatchEvent(new Event("change"));
            }
            if (timeEl) timeEl.value = time;
        });
    });

    // 6. การยืนยันก่อนลบหรือยกเลิกรายการ
    const confirmForms = document.querySelectorAll(".js-confirm-form");
    confirmForms.forEach(function (form) {
        form.addEventListener("submit", function (e) {
            const msg = form.getAttribute("data-confirm") || "คุณยืนยันที่จะดำเนินการนี้ใช่หรือไม่?";
            if (!window.confirm(msg)) {
                e.preventDefault();
            }
        });
    });
});
