/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "../internal/views/**/*.templ",
    "../internal/views/**/*_templ.go",
    "./static/**/*.html",
  ],
  // Classes montadas dinamicamente em Go (Badge/StatCard tons) precisam sobreviver
  // ao purge — Tailwind não enxerga concatenação "bg-"+tone+"-100" em string.
  safelist: [
    {
      pattern: /(bg|text|border|ring)-(sentinel|success|warning|danger|info)-(50|100|500|700|900)/,
    },
  ],
  theme: {
    extend: {
      colors: {
        sentinel: {
          50:  "#f5f7fa",
          100: "#e4e9f1",
          500: "#475569",
          700: "#1e293b",
          900: "#0b1220",
        },
        success: {
          50:  "#ecfdf5",
          100: "#d1fae5",
          500: "#10b981",
          700: "#047857",
          900: "#064e3b",
        },
        warning: {
          50:  "#fffbeb",
          100: "#fef3c7",
          500: "#f59e0b",
          700: "#b45309",
          900: "#78350f",
        },
        danger: {
          50:  "#fef2f2",
          100: "#fee2e2",
          500: "#ef4444",
          700: "#b91c1c",
          900: "#7f1d1d",
        },
        info: {
          50:  "#eff6ff",
          100: "#dbeafe",
          500: "#3b82f6",
          700: "#1d4ed8",
          900: "#1e3a8a",
        },
      },
      fontFamily: {
        sans: ["Inter", "system-ui", "sans-serif"],
        mono: ["JetBrains Mono", "ui-monospace", "monospace"],
      },
      boxShadow: {
        card: "0 1px 2px 0 rgb(15 23 42 / 0.04), 0 1px 3px 0 rgb(15 23 42 / 0.06)",
      },
    },
  },
  plugins: [],
};
