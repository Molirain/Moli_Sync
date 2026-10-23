/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./*.html",
    "./docs/**/*.html",
    "./docs-src/**/*.html",
    "./assets/js/**/*.js",
    "./scripts/**/*.mjs"
  ],
  theme: {
    extend: {
      colors: {
        ink: "#202425",
        deep: "#171A1B",
        panel: "#242829",
        "panel-hi": "#282D2E",
        line: "#343A3B",
        "line-soft": "#2B3031",
        fg: "#E7EDEC",
        muted: "#8B9694",
        faint: "#5E6866",
        accent: "#28CCC9"
      },
      fontFamily: {
        sans: [
          "Inter",
          "PingFang SC",
          "Hiragino Sans GB",
          "Microsoft YaHei",
          "system-ui",
          "sans-serif"
        ],
        mono: [
          "JetBrains Mono",
          "ui-monospace",
          "SFMono-Regular",
          "Consolas",
          "PingFang SC",
          "Microsoft YaHei",
          "monospace"
        ]
      },
      maxWidth: {
        page: "1180px"
      },
      transitionTimingFunction: {
        out: "cubic-bezier(0, 0, 0.2, 1)"
      }
    }
  },
  plugins: []
};
