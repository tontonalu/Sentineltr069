package devices

import (
	"fmt"
	"strings"
)

// buildPolyline gera atributo `points` para o SVG. Eixos:
//   - X = posição relativa no array (0..900)
//   - Y = valor escalado em 220 - normalize(v) * 220 (220px de altura útil)
//
// Quando duas séries são plotadas, ambas precisam compartilhar o mesmo
// max(yA, yB) para o eixo Y ser comparável — `buildPolylineB` recebe
// ambas e usa o mesmo escalonamento.
func buildPolyline(primary, other []SeriesPoint) string {
	yMax := maxY(primary, other)
	return polylineFor(primary, yMax)
}

func buildPolylineB(primary, other []SeriesPoint) string {
	yMax := maxY(primary, other)
	return polylineFor(other, yMax)
}

func polylineFor(s []SeriesPoint, yMax float64) string {
	if len(s) == 0 || yMax <= 0 {
		return ""
	}
	var b strings.Builder
	for i, p := range s {
		x := float64(i) / float64(maxIdx(len(s))) * 900
		y := 220 - (p.Y/yMax)*200 + 10 // 10px de margem topo
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(fmt.Sprintf("%.1f,%.1f", x, y))
	}
	return b.String()
}

func maxIdx(n int) int {
	if n <= 1 {
		return 1
	}
	return n - 1
}

func maxY(a, b []SeriesPoint) float64 {
	m := 0.0
	for _, p := range a {
		if p.Y > m {
			m = p.Y
		}
	}
	for _, p := range b {
		if p.Y > m {
			m = p.Y
		}
	}
	return m
}

// formatUptime — converte segundos para "Nd Hh Mm".
func formatUptime(seconds int64) string {
	d := seconds / 86400
	h := (seconds % 86400) / 3600
	m := (seconds % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("%dd %dh %dm", d, h, m)
	}
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func formatCPUMem(cpu, mem *float64) string {
	cpuStr := "—"
	memStr := "—"
	if cpu != nil {
		cpuStr = fmt.Sprintf("%.1f%%", *cpu)
	}
	if mem != nil {
		memStr = fmt.Sprintf("%.1f%%", *mem)
	}
	return cpuStr + " / " + memStr
}
