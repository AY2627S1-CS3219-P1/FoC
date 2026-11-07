package render

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/AY2627S1-CS3219-P1/FoC/benchmarks/token-revocation/internal/load"
)

// chartRead renders a log-log plot of read p99 latency (ms) against concurrency,
// one polyline per variant, at the read-only write fraction (0). It shows how
// the check latency scales under load with no writes injected.
func chartRead(rows []load.Result) string {
	const (
		w  = 780
		h  = 460
		mL = 96
		mR = 24
		mT = 30
		mB = 82
	)
	plotW := float64(w - mL - mR)
	plotH := float64(h - mT - mB)

	byVariant := map[string][]load.Result{}
	var xs, ys []float64
	for _, r := range rows {
		if r.WritePct > 1e-9 {
			continue
		}
		byVariant[r.Variant] = append(byVariant[r.Variant], r)
		if r.Concurrency > 0 {
			xs = append(xs, float64(r.Concurrency))
		}
		if r.Read.P99ms > 0 {
			ys = append(ys, r.Read.P99ms)
		}
	}
	if len(xs) == 0 {
		return svgFrame("no read data", "")
	}

	logMinX, logMaxX := log10(minF(xs)), log10(maxF(xs))
	logMinY := log10(minF(ys) * 0.5)
	logMaxY := log10(maxF(ys) * 1.25)

	mapX := func(c float64) float64 { return float64(mL) + (log10(c)-logMinX)/(logMaxX-logMinX)*plotW }
	mapY := func(v float64) float64 { return float64(mT+plotH) - (log10(v)-logMinY)/(logMaxY-logMinY)*plotH }

	var b strings.Builder
	write := func(s string) { b.WriteString(s) }
	write(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="system-ui, sans-serif" font-size="13">`, w, h, w, h))
	write(`<rect width="100%" height="100%" fill="white"/>`)
	write(`<text x="96" y="20" font-size="16" font-weight="700">read p99 revocation-check latency vs concurrency (no writes)</text>`)

	for p := int(math.Floor(logMinY)); p <= int(math.Ceil(logMaxY)); p++ {
		v := math.Pow(10, float64(p))
		if v < minF(ys)*0.5 || v > maxF(ys)*1.25 {
			continue
		}
		y := mapY(v)
		write(fmt.Sprintf(`<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="#e5e7eb"/>`, mL, y, w-mR, y))
		write(fmt.Sprintf(`<text x="%d" y="%.1f" text-anchor="end" dominant-baseline="middle" fill="#6b7280">%.0g</text>`, mL-8, y, v))
	}
	seen := map[float64]bool{}
	for _, r := range rows {
		if r.WritePct > 1e-9 {
			continue
		}
		c := float64(r.Concurrency)
		if seen[c] {
			continue
		}
		seen[c] = true
		x := mapX(c)
		write(fmt.Sprintf(`<line x1="%.1f" y1="%g" x2="%.1f" y2="%d" stroke="#e5e7eb"/>`, x, mT+plotH, x, mT))
		write(fmt.Sprintf(`<text x="%.1f" y="%g" text-anchor="middle" fill="#6b7280">%.0g</text>`, x, mT+plotH+16, c))
	}
	write(fmt.Sprintf(`<text x="%d" y="%d" text-anchor="middle">concurrent auth checks (log scale)</text>`, (w)/2, h-16))
	write(fmt.Sprintf(`<text x="16" y="%g" text-anchor="middle" transform="rotate(-90 16 %g)">read p99 latency (ms, log scale)</text>`, (mT+plotH)/2, (mT+plotH)/2))

	orders := []string{"redis", "pg", "http"}
	colors := map[string]string{"redis": "#2563eb", "pg": "#dc2626", "http": "#16a34a"}
	legend := map[string]string{
		"redis": "Redis dictionary",
		"pg":    "Postgres, direct",
		"http":  "Postgres via HTTP round trip",
	}
	for i, v := range orders {
		points := byVariant[v]
		if len(points) == 0 {
			continue
		}
		sortByConcurrency(points)
		var pts string
		for _, r := range points {
			pts += fmt.Sprintf("%g,%g ", mapX(float64(r.Concurrency)), mapY(r.Read.P99ms))
		}
		write(fmt.Sprintf(`<polyline points="%s" fill="none" stroke="%s" stroke-width="2.5" opacity="0.85" stroke-linejoin="round" stroke-linecap="round"/>`, pts, colors[v]))
		for _, r := range points {
			write(fmt.Sprintf(`<circle cx="%g" cy="%g" r="3.2" fill="%s"/>`, mapX(float64(r.Concurrency)), mapY(r.Read.P99ms), colors[v]))
		}
		lx, ly := w-mR-170, mT+6
		y := ly + i*16
		write(fmt.Sprintf(`<rect x="%d" y="%d" width="12" height="12" fill="%s" opacity="0.85"/>`, lx, y, colors[v]))
		write(fmt.Sprintf(`<text x="%d" y="%d" dominant-baseline="middle" fill="#374151">%s</text>`, lx+18, y+6, legend[v]))
	}
	write(`</svg>`)

	return b.String()
}

// chartWrite renders a log-log plot of write p99 ack latency (ms) against the
// injected write fraction, one polyline per variant, so the effect of durable
// writes on write latency reads at a glance.
func chartWrite(rows []load.Result) string {
	const (
		w  = 780
		h  = 460
		mL = 96
		mR = 24
		mT = 30
		mB = 82
	)
	plotW := float64(w - mL - mR)
	plotH := float64(h - mT - mB)

	byVariant := map[string][]load.Result{}
	var xs, ys []float64
	for _, r := range rows {
		if r.WritePct <= 1e-9 || r.Write.Ops == 0 {
			continue
		}
		byVariant[r.Variant] = append(byVariant[r.Variant], r)
		xs = append(xs, r.WritePct)
		if r.Write.P99ms > 0 {
			ys = append(ys, r.Write.P99ms)
		}
	}
	if len(xs) == 0 {
		return svgFrame("no write data", "")
	}

	logMinX, logMaxX := log10(minF(xs)), log10(maxF(xs))
	logMinY := log10(minF(ys) * 0.5)
	logMaxY := log10(maxF(ys) * 1.25)

	mapX := func(p float64) float64 { return float64(mL) + (log10(p)-logMinX)/(logMaxX-logMinX)*plotW }
	mapY := func(v float64) float64 { return float64(mT+plotH) - (log10(v)-logMinY)/(logMaxY-logMinY)*plotH }

	var b strings.Builder
	write := func(s string) { b.WriteString(s) }
	write(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="system-ui, sans-serif" font-size="13">`, w, h, w, h))
	write(`<rect width="100%" height="100%" fill="white"/>`)
	write(`<text x="96" y="20" font-size="16" font-weight="700">durable-write p99 ack latency vs injected write fraction</text>`)

	for p := int(math.Floor(logMinY)); p <= int(math.Ceil(logMaxY)); p++ {
		v := math.Pow(10, float64(p))
		if v < minF(ys)*0.5 || v > maxF(ys)*1.25 {
			continue
		}
		y := mapY(v)
		write(fmt.Sprintf(`<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="#e5e7eb"/>`, mL, y, w-mR, y))
		write(fmt.Sprintf(`<text x="%d" y="%.1f" text-anchor="end" dominant-baseline="middle" fill="#6b7280">%.0g</text>`, mL-8, y, v))
	}
	seen := map[float64]bool{}
	for _, r := range rows {
		if r.WritePct <= 1e-9 {
			continue
		}
		p := r.WritePct
		if seen[p] {
			continue
		}
		seen[p] = true
		x := mapX(p)
		write(fmt.Sprintf(`<line x1="%.1f" y1="%g" x2="%.1f" y2="%d" stroke="#e5e7eb"/>`, x, mT+plotH, x, mT))
		write(fmt.Sprintf(`<text x="%.1f" y="%g" text-anchor="middle" fill="#6b7280">%.2g</text>`, x, mT+plotH+16, p))
	}
	write(fmt.Sprintf(`<text x="%d" y="%d" text-anchor="middle">write fraction of ops (log scale)</text>`, (w)/2, h-16))
	write(fmt.Sprintf(`<text x="16" y="%g" text-anchor="middle" transform="rotate(-90 16 %g)">write p99 ack latency (ms, log scale)</text>`, (mT+plotH)/2, (mT+plotH)/2))

	orders := []string{"redis", "pg", "http"}
	colors := map[string]string{"redis": "#2563eb", "pg": "#dc2626", "http": "#16a34a"}
	legend := map[string]string{
		"redis": "Redis dictionary",
		"pg":    "Postgres, direct",
		"http":  "Postgres via HTTP round trip",
	}
	for i, v := range orders {
		points := byVariant[v]
		if len(points) == 0 {
			continue
		}
		sortByWrite(points)
		var pts string
		for _, r := range points {
			pts += fmt.Sprintf("%g,%g ", mapX(r.WritePct), mapY(r.Write.P99ms))
		}
		write(fmt.Sprintf(`<polyline points="%s" fill="none" stroke="%s" stroke-width="2.5" opacity="0.85" stroke-linejoin="round" stroke-linecap="round"/>`, pts, colors[v]))
		for _, r := range points {
			write(fmt.Sprintf(`<circle cx="%g" cy="%g" r="3.2" fill="%s"/>`, mapX(r.WritePct), mapY(r.Write.P99ms), colors[v]))
		}
		lx, ly := w-mR-170, mT+6
		y := ly + i*16
		write(fmt.Sprintf(`<rect x="%d" y="%d" width="12" height="12" fill="%s" opacity="0.85"/>`, lx, y, colors[v]))
		write(fmt.Sprintf(`<text x="%d" y="%d" dominant-baseline="middle" fill="#374151">%s</text>`, lx+18, y+6, legend[v]))
	}
	write(`</svg>`)

	return b.String()
}

func svgFrame(title, note string) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="400" height="120" font-family="system-ui, sans-serif"><rect width="100%%" height="100%%" fill="white"/><text x="20" y="50" font-size="16" font-weight="700">%s</text><text x="20" y="76" font-size="12" fill="#6b7280">%s</text></svg>`, title, note)
}

func sortByConcurrency(rows []load.Result) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Concurrency < rows[j].Concurrency })
}

func sortByWrite(rows []load.Result) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].WritePct < rows[j].WritePct })
}

func log10(x float64) float64 { return math.Log10(x) }

func minF(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs {
		if x < m {
			m = x
		}
	}

	return m
}

func maxF(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs {
		if x > m {
			m = x
		}
	}

	return m
}
