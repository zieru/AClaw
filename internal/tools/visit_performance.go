package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

// VisitPerformanceTool menyediakan kemampuan native untuk mengambil screenshot
// dashboard Visit Performance (https://a1.tsel.my.id/visit-performance) dan menarik data
// metrik analitik lengkap langsung dari internal API / Parquet DuckDB.
type VisitPerformanceTool struct{}

func (t *VisitPerformanceTool) Name() string {
	return "capture_visit_performance"
}

func (t *VisitPerformanceTool) Description() string {
	return "Mengambil screenshot visual dashboard Visit Performance (https://a1.tsel.my.id/visit-performance) sekaligus menarik data metrik analitik lengkap (Total Visit Area Sumatera, MoM growth, per Regional Sumbagut/Sumbagteng/Sumbagsel, serta Top & Bottom Branch/Territory beserta waiting time & serving time). Gambar dashboard otomatis dilampirkan ke chat pengguna, dan data terstruktur dikembalikan agar AI dapat menyusun analisis eksekutif komprehensif."
}

func (t *VisitPerformanceTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"month": {
				Type:        "string",
				Description: "Bulan periode yang dianalisa dalam format 'YYYY-MM' (contoh: '2026-07' atau 'July'). Kosongkan jika ingin data akumulatif atau bulan berjalan.",
			},
			"flag": {
				Type:        "string",
				Description: "Filter status layanan: 'Dilayani' (Dilayani Saja, default), 'Semua' (Semua Status), 'Batal' (Batal/Tidak Dilayani).",
				Enum:        []string{"Dilayani", "Semua", "Batal"},
			},
			"subjek_serving": {
				Type:        "string",
				Description: "Filter durasi pelayanan: '> 1 Menit' (Default, serving di atas 1 menit), 'Semua' (Semua Durasi), '<= 1 Menit'.",
				Enum:        []string{"> 1 Menit", "Semua", "<= 1 Menit"},
			},
			"capture_screenshot": {
				Type:        "boolean",
				Description: "Apakah ingin mengambil screenshot visual dashboard web https://a1.tsel.my.id/visit-performance (Default: true).",
			},
			"section": {
				Type:        "string",
				Description: "Bagian tampilan yang ingin di-capture (bebas navbar): 'overview' (seluruh dashboard bersih: KPI cards + 3 charts + 3 tabel territory, default), 'kpi' (div kartu KPI Total Area & Sumbagut/Sumbagteng/Sumbagsel: VISIT, WAITING, SERVING), 'charts' (div 3 grafik bar: VISIT, WAITING TIME, SERVING TIME), 'tables' (div 3 tabel regional: SUMBAGUT, SUMBAGTENG, SUMBAGSEL TOP TERRITORY).",
				Enum:        []string{"overview", "kpi", "charts", "tables"},
			},
		},
	}
}

// ApiResponse represents the standard GoAssist API JSON structure
type ApiResponse struct {
	Status string `json:"status"`
	Output struct {
		Rows []map[string]interface{} `json:"rows"`
	} `json:"output"`
}

func (t *VisitPerformanceTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	month := ""
	if mVal, ok := args["month"].(string); ok {
		month = normalizeMonth(mVal)
	}

	flag := "Dilayani"
	if fVal, ok := args["flag"].(string); ok && strings.TrimSpace(fVal) != "" {
		flag = strings.TrimSpace(fVal)
	}

	subjekServing := "> 1 Menit"
	if sVal, ok := args["subjek_serving"].(string); ok && strings.TrimSpace(sVal) != "" {
		subjekServing = strings.TrimSpace(sVal)
	}

	captureScreenshot := true
	if csVal, ok := args["capture_screenshot"].(bool); ok {
		captureScreenshot = csVal
	}

	section := "overview"
	if sVal, ok := args["section"].(string); ok && strings.TrimSpace(sVal) != "" {
		section = strings.ToLower(strings.TrimSpace(sVal))
	}

	// 1. Tentukan Base API URL (prioritaskan env, lalu local port 12110, fallback ke live prod)
	baseURL := getVisitApiBaseURL()

	// 2. Susun query filter SQL WHERE
	whereQuery := buildVisitWhereQuery(month, flag, subjekServing)

	// 3. Ambil data analitik secara konkuren
	client := &http.Client{Timeout: 15 * time.Second}

	var (
		totalAreaRows   []map[string]interface{}
		prevTotalRows   []map[string]interface{}
		regionalRows    []map[string]interface{}
		territoryRows   []map[string]interface{}
		fetchErr        error
		wg              sync.WaitGroup
		mu              sync.Mutex
	)

	// Fetch Total
	wg.Add(1)
	go func() {
		defer wg.Done()
		rows, err := fetchEndpointRows(ctx, client, fmt.Sprintf("%s/visit/performance-total%s", baseURL, whereQuery))
		mu.Lock()
		if err != nil && fetchErr == nil {
			fetchErr = err
		}
		totalAreaRows = rows
		mu.Unlock()
	}()

	// Fetch Regional
	wg.Add(1)
	go func() {
		defer wg.Done()
		rows, err := fetchEndpointRows(ctx, client, fmt.Sprintf("%s/visit/performance-regional%s", baseURL, whereQuery))
		mu.Lock()
		if err != nil && fetchErr == nil {
			fetchErr = err
		}
		regionalRows = rows
		mu.Unlock()
	}()

	// Fetch Territory
	wg.Add(1)
	go func() {
		defer wg.Done()
		rows, err := fetchEndpointRows(ctx, client, fmt.Sprintf("%s/visit/performance-territory%s", baseURL, whereQuery))
		mu.Lock()
		if err != nil && fetchErr == nil {
			fetchErr = err
		}
		territoryRows = rows
		mu.Unlock()
	}()

	// Fetch MoM jika month ditentukan
	prevMonth := ""
	if month != "" {
		prevMonth = getPrevMonth(month)
		if prevMonth != "" {
			prevWhere := buildVisitWhereQuery(prevMonth, flag, subjekServing)
			wg.Add(1)
			go func() {
				defer wg.Done()
				rows, _ := fetchEndpointRows(ctx, client, fmt.Sprintf("%s/visit/performance-total%s", baseURL, prevWhere))
				mu.Lock()
				prevTotalRows = rows
				mu.Unlock()
			}()
		}
	}

	wg.Wait()

	// 4. Capture screenshot via browser jika diminta (bebas navbar)
	var screenshotTag string
	var screenshotErr string
	if captureScreenshot {
		targetURL := "https://a1.tsel.my.id/visit-performance"
		if month != "" {
			targetURL = fmt.Sprintf("https://a1.tsel.my.id/visit-performance?month=%s", url.QueryEscape(month))
		}
		scPath, err := captureWebScreenshot(ctx, targetURL, section, month)
		if err != nil {
			screenshotErr = fmt.Sprintf("⚠️ <i>Gagal mengambil snapshot web: %v</i>", err)
		} else if scPath != "" {
			sectionCaption := "Dashboard Visit Performance (Area Sumatera)"
			switch section {
			case "kpi":
				sectionCaption = "KPI Cards (VISIT, WAITING & SERVING TIME)"
			case "charts":
				sectionCaption = "Grafik Bar (VISIT, WAITING & SERVING TIME)"
			case "tables":
				sectionCaption = "Top Territory Tables (SUMBAGUT, SUMBAGTENG, SUMBAGSEL)"
			}
			if month != "" {
				sectionCaption += fmt.Sprintf(" - Periode %s", month)
			}
			screenshotTag = fmt.Sprintf("[ATTACH_FILE:%s|CAPTION:%s]", scPath, sectionCaption)
		}
	}

	// 5. Susun laporan terstruktur untuk LLM
	var sb strings.Builder

	if screenshotTag != "" {
		sb.WriteString(screenshotTag)
		sb.WriteString("\n\n")
	} else if screenshotErr != "" {
		sb.WriteString(screenshotErr)
		sb.WriteString("\n\n")
	}

	sb.WriteString("### 📊 DATA METRIK VISIT PERFORMANCE (https://a1.tsel.my.id/visit-performance)\n\n")
	sb.WriteString(fmt.Sprintf("- **Periode**: %s\n", map[bool]string{true: month, false: "Semua Bulan / Akumulatif"}[month != ""]))
	sb.WriteString(fmt.Sprintf("- **Filter Status**: %s\n", flag))
	sb.WriteString(fmt.Sprintf("- **Filter Serving**: %s\n\n", subjekServing))

	// Data Total Area
	var totalVisit int64
	var avgWaiting, avgServing float64
	if len(totalAreaRows) > 0 {
		row := totalAreaRows[0]
		totalVisit = getInt64(row["total_visit"])
		avgWaiting = getFloat64(row["avg_waiting_minutes"])
		avgServing = getFloat64(row["avg_serving_minutes"])
	}

	sb.WriteString("#### 1. Total Area Sumatera\n")
	sb.WriteString(fmt.Sprintf("- **Total Kunjungan (Visit)**: %s\n", formatNumber(totalVisit)))
	sb.WriteString(fmt.Sprintf("- **Rata-rata Waktu Tunggu (Waiting Time)**: %.2f menit\n", avgWaiting))
	sb.WriteString(fmt.Sprintf("- **Rata-rata Waktu Layan (Serving Time)**: %.2f menit\n", avgServing))

	// Hitung MoM jika ada data bulan sebelumnya
	if len(prevTotalRows) > 0 {
		prevVisit := getInt64(prevTotalRows[0]["total_visit"])
		if prevVisit > 0 {
			diff := float64(totalVisit - prevVisit)
			pct := (diff / float64(prevVisit)) * 100
			direction := "🔺 Naik"
			if pct < 0 {
				direction = "🔻 Turun"
			}
			sb.WriteString(fmt.Sprintf("- **Perbandingan MoM (vs %s)**: %s %.2f%% (M-1: %s kunjungan)\n",
				prevMonth, direction, pct, formatNumber(prevVisit)))
		}
	}
	sb.WriteString("\n")

	// Data Per Regional
	sb.WriteString("#### 2. Performansi per Regional\n")
	sb.WriteString("| Regional | Total Visit | Share (%) | Avg Waiting (Min) | Avg Serving (Min) |\n")
	sb.WriteString("| :--- | :---: | :---: | :---: | :---: |\n")

	for _, r := range regionalRows {
		regName := getString(r["regional"])
		rVisit := getInt64(r["total_visit"])
		rWait := getFloat64(r["avg_waiting_minutes"])
		rServ := getFloat64(r["avg_serving_minutes"])

		sharePct := 0.0
		if totalVisit > 0 {
			sharePct = (float64(rVisit) / float64(totalVisit)) * 100
		}

		sb.WriteString(fmt.Sprintf("| **%s** | %s | %.1f%% | %.2f m | %.2f m |\n",
			regName, formatNumber(rVisit), sharePct, rWait, rServ))
	}
	sb.WriteString("\n")

	// Filter & Sort Territory (Top 5 & Waiting Time Tertinggi)
	type territoryItem struct {
		name       string
		regional   string
		visit      int64
		avgWaiting float64
		avgServing float64
	}

	var tList []territoryItem
	for _, tr := range territoryRows {
		tName := getString(tr["territory"])
		if tName == "" || strings.EqualFold(tName, "null") {
			continue
		}
		tList = append(tList, territoryItem{
			name:       tName,
			regional:   getString(tr["regional"]),
			visit:      getInt64(tr["total_visit"]),
			avgWaiting: getFloat64(tr["avg_waiting_minutes"]),
			avgServing: getFloat64(tr["avg_serving_minutes"]),
		})
	}

	// Sort by Visit desc (Top branches)
	sort.Slice(tList, func(i, j int) bool {
		return tList[i].visit > tList[j].visit
	})

	sb.WriteString("#### 3. Top 5 Territory / Branch Kunjungan Tertinggi\n")
	topN := 5
	if len(tList) < topN {
		topN = len(tList)
	}
	for i := 0; i < topN; i++ {
		item := tList[i]
		sb.WriteString(fmt.Sprintf("%d. **%s** (%s): %s visit | Wait: %.2f m | Serve: %.2f m\n",
			i+1, item.name, item.regional, formatNumber(item.visit), item.avgWaiting, item.avgServing))
	}
	sb.WriteString("\n")

	// Sort by Waiting Time desc (Bottlenecks / Antrean Lama)
	var waitList []territoryItem
	for _, item := range tList {
		if item.visit >= 500 && item.avgWaiting > 0 { // minimal volume agar representatif
			waitList = append(waitList, item)
		}
	}
	sort.Slice(waitList, func(i, j int) bool {
		return waitList[i].avgWaiting > waitList[j].avgWaiting
	})

	sb.WriteString("#### 4. Territory / Branch dengan Waktu Tunggu Terlama (Bottleneck Focus)\n")
	botN := 5
	if len(waitList) < botN {
		botN = len(waitList)
	}
	for i := 0; i < botN; i++ {
		item := waitList[i]
		sb.WriteString(fmt.Sprintf("- ⚠️ **%s** (%s): **%.2f menit** (Visit: %s, Serve: %.2f m)\n",
			item.name, item.regional, item.avgWaiting, formatNumber(item.visit), item.avgServing))
	}

	sb.WriteString("\n---\n*Instruksi untuk AI: Buatlah laporan analisis eksekutif komprehensif, evaluasi pencapaian SLA antrean (< 5 menit), perbandingan antar regional, soroti cabang bottleneck, dan berikan rekomendasi actionable.*")

	return sb.String(), nil
}

// ─── Helpers ───────────────────────────────────────────────────────────────────

func getVisitApiBaseURL() string {
	if env := os.Getenv("GOASSIST_API_URL"); strings.TrimSpace(env) != "" {
		return strings.TrimRight(strings.TrimSpace(env), "/")
	}

	// Cek cepat localhost port 12110
	client := &http.Client{Timeout: 500 * time.Millisecond}
	if resp, err := client.Get("http://127.0.0.1:12110/api/visit/performance-total"); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode == 200 {
			return "http://127.0.0.1:12110/api"
		}
	}

	// Fallback ke production domain
	return "https://goassist.tsel.my.id/api"
}

func buildVisitWhereQuery(month, flag, subjekServing string) string {
	var conditions []string

	if flag == "Dilayani" {
		conditions = append(conditions, "(flag_dilayani = 1 or flag_dilayani = '1' or lower(cast(flag_dilayani as varchar)) like '%dilayani%')")
	} else if flag == "Batal" {
		conditions = append(conditions, "(flag_dilayani = 0 or flag_dilayani = '0' or lower(cast(flag_dilayani as varchar)) like '%batal%' or lower(cast(flag_dilayani as varchar)) like '%tidak%')")
	}

	if subjekServing == "> 1 Menit" {
		conditions = append(conditions, "(\"Subjek Serving\" = '> 1 Menit' or cast(\"Subjek Serving\" as varchar) LIKE '%> 1%')")
	} else if subjekServing == "<= 1 Menit" {
		conditions = append(conditions, "(\"Subjek Serving\" = '<= 1 Menit' or cast(\"Subjek Serving\" as varchar) LIKE '%<= 1%')")
	}

	if month != "" {
		conditions = append(conditions, fmt.Sprintf("cast(\"Trx Date\" as varchar) LIKE '%s%%'", month))
	}

	if len(conditions) == 0 {
		return ""
	}

	return "?where=" + url.QueryEscape(strings.Join(conditions, " AND "))
}

func fetchEndpointRows(ctx context.Context, client *http.Client, targetURL string) ([]map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "GoAssistant/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var parsed ApiResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}

	return parsed.Output.Rows, nil
}

func captureWebScreenshot(ctx context.Context, targetURL string, section string, targetMonth string) (string, error) {
	if globalBrowserSession == nil {
		return "", fmt.Errorf("globalBrowserSession tidak tersedia")
	}

	// Buka halaman dan tunggu 3 detik awal agar animasi Vue + ECharts selesai
	page, err := globalBrowserSession.GetPage(ctx, targetURL, 3, true)
	if err != nil {
		return "", err
	}

	// Set viewport desktop 1680px agar 5 kartu KPI, 3 charts, dan 3 tables muat berdampingan tanpa terpotong
	_ = page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width:             1680,
		Height:            1050,
		DeviceScaleFactor: 1.5,
		Mobile:            false,
	})

	// Jika targetMonth dispesifikasikan, sinkronkan dropdown Vuetify di browser ke bulan tersebut
	if targetMonth != "" {
		monthKeywords := getMonthKeywords(targetMonth)
		if selEl, errSel := page.Element(".header-month-select"); errSel == nil && selEl != nil {
			_ = selEl.Click(proto.InputMouseButtonLeft, 1)
			time.Sleep(500 * time.Millisecond)

			kwJSON, _ := json.Marshal(monthKeywords)
			selectScript := fmt.Sprintf(`() => {
				const keywords = %s;
				const items = Array.from(document.querySelectorAll('.v-overlay .v-list-item, .v-list-item'));
				for (const kw of keywords) {
					const target = items.find(it => it.innerText.toLowerCase().includes(kw.toLowerCase()));
					if (target) {
						target.click();
						return target.innerText;
					}
				}
				return null;
			}`, string(kwJSON))
			_, _ = page.Eval(selectScript)
			// Beri jeda 2.5 detik agar fetch data baru dan re-render chart ECharts & tabel selesai
			time.Sleep(2500 * time.Millisecond)
		}
	}

	// 1. Bersihkan navbar dan rapikan layout agar tidak terpotong horizontal & vertikal
	prepareScript := `() => {
		// Hilangkan navbar aplikasi (header.v-app-bar), navigation drawer, dan switch-view button
		const navbars = document.querySelectorAll('.v-app-bar, header, nav, .v-navigation-drawer, .switch-view-btn');
		navbars.forEach(el => el.style.setProperty('display', 'none', 'important'));

		// Reset padding & margin top pada v-main dan visit-perf-view agar konten menempel bersih ke atas
		document.querySelectorAll('.v-main, .visit-perf-view').forEach(el => {
			el.style.setProperty('padding-top', '0px', 'important');
			el.style.setProperty('margin-top', '0px', 'important');
			el.style.setProperty('overflow', 'visible', 'important');
		});

		// Pastikan KPI Row (Total Area + Sumatera + Sumbagut + Sumbagteng + Sumbagsel) tidak overflow atau terpotong ke kanan
		const kpiRow = document.querySelector('.perf-kpi-row');
		if (kpiRow) {
			kpiRow.style.setProperty('display', 'flex', 'important');
			kpiRow.style.setProperty('flex-wrap', 'nowrap', 'important');
			kpiRow.style.setProperty('overflow', 'visible', 'important');
			kpiRow.style.setProperty('gap', '10px', 'important');
		}

		// Sesuaikan kartu KPI agar 5 kartu pas sempurna dalam satu baris di 1680px
		document.querySelectorAll('.perf-kpi-total-card').forEach(el => {
			el.style.setProperty('min-width', '170px', 'important');
			el.style.setProperty('flex', '1 1 170px', 'important');
		});
		document.querySelectorAll('.perf-kpi-regional-card').forEach(el => {
			el.style.setProperty('min-width', '180px', 'important');
			el.style.setProperty('flex', '1 1 180px', 'important');
		});

		// Tandai container section KPI Cards
		const kpiHeader = document.querySelector('.perf-header');
		if (kpiHeader) kpiHeader.id = '__ga_section_kpi';

		// Rapikan 3 Bar Charts (VISIT, WAITING, SERVING) agar berjajar 3 kolom penuh
		const chartCards = document.querySelectorAll('.perf-chart-card');
		if (chartCards.length > 0) {
			const chartRow = chartCards[0].closest('.v-row') || chartCards[0].parentElement?.parentElement;
			if (chartRow) {
				chartRow.id = '__ga_section_charts';
				chartRow.style.setProperty('display', 'flex', 'important');
				chartRow.style.setProperty('flex-wrap', 'nowrap', 'important');
				chartRow.style.setProperty('overflow', 'visible', 'important');
			}
			chartCards.forEach(c => {
				const col = c.closest('.v-col') || c.parentElement;
				if (col) {
					col.style.setProperty('flex', '0 0 33.333%', 'important');
					col.style.setProperty('max-width', '33.333%', 'important');
				}
			});
		}

		// Rapikan 3 Tables (SUMBAGUT, SUMBAGTENG, SUMBAGSEL TOP TERRITORY) agar berjajar 3 kolom penuh
		const tableCards = document.querySelectorAll('.perf-table-card');
		if (tableCards.length > 0) {
			const tableRow = tableCards[0].closest('.v-row') || tableCards[0].parentElement?.parentElement;
			if (tableRow) {
				tableRow.id = '__ga_section_tables';
				tableRow.style.setProperty('display', 'flex', 'important');
				tableRow.style.setProperty('flex-wrap', 'nowrap', 'important');
				tableRow.style.setProperty('overflow', 'visible', 'important');
			}
			tableCards.forEach(t => {
				const col = t.closest('.v-col') || t.parentElement;
				if (col) {
					col.style.setProperty('flex', '0 0 33.333%', 'important');
					col.style.setProperty('max-width', '33.333%', 'important');
				}
			});
		}

		return true;
	}`

	_, _ = page.Eval(prepareScript)
	time.Sleep(600 * time.Millisecond)

	screenshotDir := filepath.Join("data", "screenshots")
	_ = os.MkdirAll(screenshotDir, 0755)
	cleanupOldScreenshots(screenshotDir, 24*time.Hour)

	outPath := filepath.Join(screenshotDir, fmt.Sprintf("visit_perf_%s_%d.png", section, time.Now().UnixNano()))
	absOutPath, _ := filepath.Abs(outPath)

	var imgBytes []byte

	// Jika bagian spesifik diminta (kpi, charts, tables), ambil langsung bounding box elemennya
	if section != "" && section != "overview" {
		targetSelector := ""
		switch section {
		case "kpi":
			targetSelector = "#__ga_section_kpi"
		case "charts":
			targetSelector = "#__ga_section_charts"
		case "tables":
			targetSelector = "#__ga_section_tables"
		}

		if targetSelector != "" {
			if el, errEl := page.Element(targetSelector); errEl == nil && el != nil {
				imgBytes, _ = el.Screenshot(proto.PageCaptureScreenshotFormatPng, 0)
			}
		}
	}

	// Default / overview: ambil full page secara utuh dari atas hingga bawah (tidak terpotong vertikal)
	if len(imgBytes) == 0 {
		imgBytes, err = page.Screenshot(true, &proto.PageCaptureScreenshot{
			Format: proto.PageCaptureScreenshotFormatPng,
		})
	}
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(absOutPath, imgBytes, 0644); err != nil {
		return "", err
	}

	return absOutPath, nil
}

func getPrevMonth(ym string) string {
	parts := strings.Split(strings.TrimSpace(ym), "-")
	if len(parts) != 2 {
		return ""
	}
	y, errY := strconv.Atoi(parts[0])
	m, errM := strconv.Atoi(parts[1])
	if errY != nil || errM != nil {
		return ""
	}
	if m == 1 {
		y--
		m = 12
	} else {
		m--
	}
	return fmt.Sprintf("%04d-%02d", y, m)
}

func getInt64(v interface{}) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	default:
		return 0
	}
}

func getFloat64(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	default:
		return 0.0
	}
}

func getString(v interface{}) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func formatNumber(n int64) string {
	in := strconv.FormatInt(n, 10)
	var out []byte
	l := len(in)
	for i, c := range in {
		if i > 0 && (l-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

func normalizeMonth(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" || raw == "semua" || raw == "all" || raw == "current" {
		return ""
	}

	// Format YYYY-MM (e.g. 2026-07)
	if reMatch := regexp.MustCompile(`^(\d{4})[-/.](\d{1,2})$`).FindStringSubmatch(raw); len(reMatch) == 3 {
		m, _ := strconv.Atoi(reMatch[2])
		return fmt.Sprintf("%s-%02d", reMatch[1], m)
	}

	// Format MM-YYYY (e.g. 07-2026)
	if reMatch := regexp.MustCompile(`^(\d{1,2})[-/.](\d{4})$`).FindStringSubmatch(raw); len(reMatch) == 3 {
		m, _ := strconv.Atoi(reMatch[1])
		return fmt.Sprintf("%s-%02d", reMatch[2], m)
	}

	// Extract year if specified (default to current year 2026)
	year := "2026"
	if reYear := regexp.MustCompile(`\b(202\d)\b`).FindStringSubmatch(raw); len(reYear) == 2 {
		year = reYear[1]
	}

	monthMap := map[string]string{
		"jan": "01", "januari": "01", "january": "01",
		"feb": "02", "februari": "02", "february": "02",
		"mar": "03", "maret": "03", "march": "03",
		"apr": "04", "april": "04",
		"mei": "05", "may": "05",
		"jun": "06", "juni": "06", "june": "06",
		"jul": "07", "juli": "07", "july": "07",
		"agt": "08", "aug": "08", "agustus": "08", "august": "08",
		"sep": "09", "september": "09",
		"okt": "10", "oct": "10", "oktober": "10", "october": "10",
		"nov": "11", "nop": "11", "november": "11",
		"des": "12", "dec": "12", "desember": "12", "december": "12",
	}

	// Prioritize longer matches first to avoid prefix collisions
	candidates := []string{
		"september", "november", "desember", "december", "februari", "february",
		"januari", "january", "agustus", "august", "oktober", "october",
		"maret", "march", "april", "juni", "june", "juli", "july",
		"sep", "nov", "nop", "des", "dec", "feb", "jan", "agt", "aug", "okt", "oct", "mar", "apr", "mei", "may", "jun", "jul",
	}

	for _, name := range candidates {
		if strings.Contains(raw, name) {
			return fmt.Sprintf("%s-%s", year, monthMap[name])
		}
	}

	return raw
}

func getMonthKeywords(ym string) []string {
	parts := strings.Split(strings.TrimSpace(ym), "-")
	if len(parts) != 2 {
		return []string{ym}
	}
	y := parts[0]
	m := parts[1]

	idNames := map[string]string{
		"01": "Januari", "02": "Februari", "03": "Maret", "04": "April",
		"05": "Mei", "06": "Juni", "07": "Juli", "08": "Agustus",
		"09": "September", "10": "Oktober", "11": "November", "12": "Desember",
	}
	name, ok := idNames[m]
	if !ok {
		return []string{ym}
	}
	return []string{
		fmt.Sprintf("%s %s", name, y), // "Juli 2026"
		name,                          // "Juli"
	}
}

