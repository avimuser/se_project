all: report.pdf

report.pdf: report.typ $(wildcard assets/*.png)
	typst compile report.typ report.pdf

.PHONY: all
