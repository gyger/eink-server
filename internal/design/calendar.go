package design

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"time"
)

type calendarLocale struct {
	months   [12]string
	weekdays [7]string
}

var calendarLocales = map[string]calendarLocale{
	"de-DE": {months: [12]string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"}, weekdays: [7]string{"Mo", "Di", "Mi", "Do", "Fr", "Sa", "So"}},
	"en-GB": {months: [12]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}, weekdays: [7]string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}},
}

func emitCalendar(enc *xml.Encoder, attrs map[string]string, values Values) ([]Rect, error) {
	date, err := time.Parse("2006-01-02", values["system.date"])
	if err != nil {
		return nil, errors.New("calendar requires system.date in YYYY-MM-DD format")
	}
	today := date
	locale, ok := calendarLocales[values["system.locale"]]
	if !ok {
		return nil, fmt.Errorf("unsupported calendar locale %q", values["system.locale"])
	}
	x, y, w, h, err := calendarGeometry(attrs)
	if err != nil {
		return nil, err
	}
	weekStart := attrs["data-week-start"]
	if weekStart == "" {
		weekStart = "monday"
	}
	if weekStart != "monday" && weekStart != "sunday" {
		return nil, errors.New("calendar data-week-start must be monday or sunday")
	}
	spillover := attrs["data-spillover"]
	if spillover == "" {
		spillover = "true"
	}
	if spillover != "true" && spillover != "false" {
		return nil, errors.New("calendar data-spillover must be true or false")
	}
	navigation := attrs["data-navigation"]
	if navigation != "" && navigation != "true" && navigation != "false" {
		return nil, errors.New("calendar data-navigation must be true or false")
	}
	monthOffset := 0
	if navigation == "true" {
		monthOffset, err = strconv.Atoi(values["widget."+attrs["id"]+".month_offset"])
		if values["widget."+attrs["id"]+".month_offset"] != "" && err != nil {
			return nil, errors.New("calendar month offset must be an integer")
		}
		if monthOffset < -12 || monthOffset > 12 {
			return nil, errors.New("calendar month offset outside supported range")
		}
		date = date.AddDate(0, monthOffset, 0)
	}

	if err := textToken(enc, x+w/2, y+h*.08, locale.months[date.Month()-1]+" "+strconv.Itoa(date.Year()), "calendar-title", "middle", h*.065, "black"); err != nil {
		return nil, err
	}
	var regions []Rect
	if navigation == "true" {
		base := Rect{Recipient: "widget", Provider: "calendar", Instance: attrs["id"]}
		prev, today, next := base, base, base
		hitTop := maxFloat(0, y-h*.1)
		hitHeight := y + h*.14 - hitTop
		outerPad := h * .05
		prev.X, prev.Y, prev.Width, prev.Height, prev.Event = int(maxFloat(0, x-outerPad)), int(hitTop), int(w*.2+outerPad), int(hitHeight), "previous"
		today.X, today.Y, today.Width, today.Height, today.Event = int(x+w*.2), int(hitTop), int(w*.6), int(hitHeight), "today"
		next.X, next.Y, next.Width, next.Height, next.Event = int(x+w*.8), int(hitTop), int(w*.2+outerPad), int(hitHeight), "next"
		if monthOffset > -12 {
			if err := textToken(enc, x+w*.08, y+h*.08, "‹", "calendar-navigation calendar-previous", "middle", h*.052, "black"); err != nil {
				return nil, err
			}
			regions = append(regions, prev)
		}
		regions = append(regions, today)
		if monthOffset < 12 {
			if err := textToken(enc, x+w*.92, y+h*.08, "›", "calendar-navigation calendar-next", "middle", h*.052, "black"); err != nil {
				return nil, err
			}
			regions = append(regions, next)
		}
	}
	colW, top, rowH := w/7, y+h*.20, h*.13
	for col := 0; col < 7; col++ {
		idx := col
		if weekStart == "sunday" {
			idx = (col + 6) % 7
		}
		if err := textToken(enc, x+(float64(col)+.5)*colW, y+h*.17, locale.weekdays[idx], "calendar-weekday", "middle", h*.035, "black"); err != nil {
			return nil, err
		}
	}
	first := time.Date(date.Year(), date.Month(), 1, 12, 0, 0, 0, time.UTC)
	offset := (int(first.Weekday()) + 6) % 7
	if weekStart == "sunday" {
		offset = int(first.Weekday())
	}
	start := first.AddDate(0, 0, -offset)
	dayFontSize := h * .043
	for cell := 0; cell < 42; cell++ {
		day := start.AddDate(0, 0, cell)
		outside := day.Month() != date.Month()
		if outside && spillover == "false" {
			continue
		}
		col, row := cell%7, cell/7
		cx, cy := x+(float64(col)+.5)*colW, top+(float64(row)+.5)*rowH
		isToday := day.Year() == today.Year() && day.Month() == today.Month() && day.Day() == today.Day()
		if isToday {
			// SVG text uses a baseline rather than a visual center. Position the
			// circle around the approximate cap-height center of the day number.
			if err := circleToken(enc, cx, cy-dayFontSize*.34, minFloat(colW*.32, rowH*.32), "calendar-today"); err != nil {
				return nil, err
			}
		}
		class := "calendar-day"
		if outside {
			class += " calendar-outside"
		}
		if isToday {
			class += " calendar-today-text"
		}
		fill := "black"
		if outside {
			fill = "#999999"
		} else if isToday {
			fill = "white"
		}
		if err := textToken(enc, cx, cy, strconv.Itoa(day.Day()), class, "middle", dayFontSize, fill); err != nil {
			return nil, err
		}
	}
	return regions, nil
}

func calendarGeometry(attrs map[string]string) (float64, float64, float64, float64, error) {
	read := func(name string) (float64, error) {
		v, err := strconv.ParseFloat(attrs["data-"+name], 64)
		if err != nil {
			return 0, fmt.Errorf("calendar data-%s must be a number", name)
		}
		return v, nil
	}
	x, err := read("x")
	if err != nil {
		return 0, 0, 0, 0, err
	}
	y, err := read("y")
	if err != nil {
		return 0, 0, 0, 0, err
	}
	w, err := read("width")
	if err != nil {
		return 0, 0, 0, 0, err
	}
	h, err := read("height")
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if w <= 0 || h <= 0 {
		return 0, 0, 0, 0, errors.New("calendar width and height must be positive")
	}
	return x, y, w, h, nil
}

func textToken(enc *xml.Encoder, x, y float64, value, class, anchor string, size float64, fill string) error {
	start := xml.StartElement{Name: xml.Name{Local: "text"}, Attr: []xml.Attr{
		{Name: xml.Name{Local: "x"}, Value: fmtFloat(x)}, {Name: xml.Name{Local: "y"}, Value: fmtFloat(y)},
		{Name: xml.Name{Local: "class"}, Value: class}, {Name: xml.Name{Local: "text-anchor"}, Value: anchor},
		{Name: xml.Name{Local: "font-size"}, Value: fmtFloat(size)}, {Name: xml.Name{Local: "fill"}, Value: fill},
	}}
	if err := enc.EncodeToken(start); err != nil {
		return err
	}
	if err := enc.EncodeToken(xml.CharData(value)); err != nil {
		return err
	}
	return enc.EncodeToken(start.End())
}

func circleToken(enc *xml.Encoder, cx, cy, r float64, class string) error {
	start := xml.StartElement{Name: xml.Name{Local: "circle"}, Attr: []xml.Attr{
		{Name: xml.Name{Local: "cx"}, Value: fmtFloat(cx)}, {Name: xml.Name{Local: "cy"}, Value: fmtFloat(cy)},
		{Name: xml.Name{Local: "r"}, Value: fmtFloat(r)}, {Name: xml.Name{Local: "class"}, Value: class}, {Name: xml.Name{Local: "fill"}, Value: "black"},
	}}
	if err := enc.EncodeToken(start); err != nil {
		return err
	}
	return enc.EncodeToken(start.End())
}

func fmtFloat(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
