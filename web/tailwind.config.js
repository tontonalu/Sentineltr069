/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "../internal/views/**/*.templ",
    "../internal/views/**/*_templ.go",
    "./static/**/*.html",
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
      },
      fontFamily: {
        sans: ["Inter", "system-ui", "sans-serif"],
        mono: ["JetBrains Mono", "ui-monospace", "monospace"],
      },
    },
  },
  plugins: [],
};
