package design

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers/rasterizer"
	draw2 "golang.org/x/image/draw"
)

const MaxSVGBytes = 2 << 20
const rasterScale = 3

var variablePattern = regexp.MustCompile(`\$\{([a-z][a-z0-9_.]*)\}`)

type Values map[string]string

type Rect struct {
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Name      string `json:"name,omitempty"`
	Action    string `json:"action,omitempty"`
	Region    string `json:"region,omitempty"`
	Recipient string `json:"recipient,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Instance  string `json:"instance,omitempty"`
	Event     string `json:"event,omitempty"`
	Order     int    `json:"order"`
}

type Output struct {
	PNG          []byte        `json:"-"`
	Actions      []Rect        `json:"actions"`
	Regions      []Rect        `json:"regions"`
	Dependencies []string      `json:"dependencies"`
	PageID       string        `json:"page_id"`
	Refresh      time.Duration `json:"refresh"`
}

type Compiler struct{}

func (Compiler) Render(source []byte, width, height int, values Values) (Output, error) {
	return (Compiler{}).RenderWithOptions(source, width, height, values, RenderOptions{Smooth: true})
}

type RenderOptions struct {
	Smooth bool
}

func (Compiler) RenderWithOptions(source []byte, width, height int, values Values, options RenderOptions) (Output, error) {
	if err := ensureFonts(); err != nil {
		return Output{}, fmt.Errorf("configure fonts: %w", err)
	}
	if len(source) == 0 || len(source) > MaxSVGBytes {
		return Output{}, fmt.Errorf("SVG size must be between 1 byte and %d bytes", MaxSVGBytes)
	}
	if width <= 0 || height <= 0 {
		return Output{}, errors.New("invalid output dimensions")
	}
	clean, meta, err := compileXML(source, width, height, values)
	if err != nil {
		return Output{}, err
	}
	c, err := parseCanvasSVG(clean)
	if err != nil {
		return Output{}, fmt.Errorf("render SVG: %w", err)
	}
	if c.W <= 0 || c.H <= 0 {
		return Output{}, errors.New("SVG has invalid rendered dimensions")
	}
	scale := 1
	if options.Smooth {
		scale = rasterScale
	}
	res := canvas.DPMM(float64(scale) * math.Min(float64(width)/c.W, float64(height)/c.H))
	raw := rasterizer.Draw(c, res, canvas.DefaultColorSpace)
	hi := image.NewRGBA(image.Rect(0, 0, width*scale, height*scale))
	draw.Draw(hi, hi.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	off := image.Pt((hi.Bounds().Dx()-raw.Bounds().Dx())/2, (hi.Bounds().Dy()-raw.Bounds().Dy())/2)
	draw.Draw(hi, raw.Bounds().Add(off), raw, raw.Bounds().Min, draw.Over)
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	if options.Smooth {
		draw2.CatmullRom.Scale(dst, dst.Bounds(), hi, hi.Bounds(), draw2.Src, nil)
	} else {
		draw.Draw(dst, dst.Bounds(), hi, hi.Bounds().Min, draw.Src)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, dst); err != nil {
		return Output{}, err
	}
	meta.PNG = encoded.Bytes()
	return meta, nil
}

func parseCanvasSVG(source []byte) (parsed *canvas.Canvas, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("SVG renderer rejected input: %v", recovered)
		}
	}()
	return canvas.ParseSVG(bytes.NewReader(source))
}

type matrix [6]float64

var identity = matrix{1, 0, 0, 1, 0, 0}

func (m matrix) mul(n matrix) matrix {
	return matrix{m[0]*n[0] + m[2]*n[1], m[1]*n[0] + m[3]*n[1], m[0]*n[2] + m[2]*n[3], m[1]*n[2] + m[3]*n[3], m[0]*n[4] + m[2]*n[5] + m[4], m[1]*n[4] + m[3]*n[5] + m[5]}
}

func (m matrix) point(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

type state struct {
	matrix  matrix
	skip    bool
	dynamic bool
	widget  bool
	name    string
}

func compileXML(source []byte, width, height int, values Values) ([]byte, Output, error) {
	dec := xml.NewDecoder(bytes.NewReader(source))
	dec.Strict = true
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	stack := []state{{matrix: identity}}
	var out Output
	deps := map[string]struct{}{}
	rootSeen, firstPage := false, false
	var viewX, viewY, viewW, viewH float64
	order := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, out, fmt.Errorf("parse SVG: %w", err)
		}
		parent := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.Directive:
			return nil, out, errors.New("SVG directives and document types are not allowed")
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if forbiddenElement(name) {
				return nil, out, fmt.Errorf("SVG element %q is not allowed", name)
			}
			t.Attr = normalizeFontAttrs(t.Attr)
			attrs := attrsMap(t.Attr)
			if parent.dynamic {
				return nil, out, errors.New("dynamic elements must not contain child elements")
			}
			child := state{matrix: parent.matrix, skip: parent.skip, name: name}
			if page := attrs["data-page"]; page != "" && !parent.skip {
				if firstPage {
					child.skip = true
				} else {
					firstPage = true
					out.PageID = page
				}
			}
			if tr := attrs["transform"]; tr != "" {
				parsed, err := parseTransform(tr)
				if err != nil {
					return nil, out, err
				}
				child.matrix = parent.matrix.mul(parsed)
			}
			if !rootSeen {
				if name != "svg" {
					return nil, out, errors.New("root element must be svg")
				}
				rootSeen = true
				vb, err := numbers(attrs["viewBox"], 4)
				if err != nil || vb[2] <= 0 || vb[3] <= 0 {
					return nil, out, errors.New("SVG requires a positive viewBox")
				}
				viewX, viewY, viewW, viewH = vb[0], vb[1], vb[2], vb[3]
				t.Attr = setAttr(t.Attr, "width", strconv.Itoa(width))
				t.Attr = setAttr(t.Attr, "height", strconv.Itoa(height))
				if attrs["font-family"] == "" {
					t.Attr = setAttr(t.Attr, "font-family", "Noto Sans")
				}
				if refresh := attrs["data-refresh"]; refresh != "" {
					d, err := time.ParseDuration(refresh)
					if err != nil || d < time.Minute || d > 24*time.Hour || d%time.Minute != 0 {
						return nil, out, errors.New("data-refresh must be a whole-minute duration from 1m through 24h")
					}
					out.Refresh = d
				}
			}
			if err := validateAttrs(t.Attr); err != nil {
				return nil, out, err
			}
			if value := attrs["data-value"]; value != "" {
				if name != "text" {
					return nil, out, errors.New("data-value is only supported on text elements")
				}
				replaced, used, err := substitute(value, values)
				if err != nil {
					return nil, out, err
				}
				for _, key := range used {
					deps[key] = struct{}{}
				}
				attrs["_dynamic_text"] = replaced
				child.dynamic = true
			}
			if widget := attrs["data-widget"]; widget != "" {
				if name != "g" {
					return nil, out, errors.New("data-widget is only supported on g elements")
				}
				if widget != "calendar" {
					return nil, out, fmt.Errorf("unknown widget %q", widget)
				}
				child.dynamic, child.widget = true, true
				if attrs["data-navigation"] == "true" {
					if attrs["id"] == "" {
						return nil, out, errors.New("navigable calendar requires an id")
					}
					if out.Refresh == 0 || out.Refresh > time.Minute {
						out.Refresh = time.Minute
					}
					deps["widget."+attrs["id"]+".month_offset"] = struct{}{}
				}
				deps["system.date"] = struct{}{}
				deps["system.locale"] = struct{}{}
			}
			if !child.skip && rootSeen && (attrs["data-action"] != "" || attrs["data-region"] != "") {
				r, err := elementRect(name, attrs)
				if err != nil {
					return nil, out, err
				}
				r = transformRect(r, child.matrix, viewX, viewY, viewW, viewH, width, height)
				r.Name, r.Action, r.Region, r.Recipient, r.Order = attrs["id"], attrs["data-action"], attrs["data-region"], "webhook", order
				order++
				if r.Action != "" {
					out.Actions = append(out.Actions, r)
				}
				if r.Region != "" {
					out.Regions = append(out.Regions, r)
				}
			}
			stack = append(stack, child)
			if !child.skip {
				t.Attr = stripDataAttrs(t.Attr)
				if err := enc.EncodeToken(t); err != nil {
					return nil, out, err
				}
				if child.dynamic {
					if child.widget {
						regions, err := emitCalendar(enc, attrs, values)
						if err != nil {
							return nil, out, err
						}
						for _, region := range regions {
							region = transformRect(region, child.matrix, viewX, viewY, viewW, viewH, width, height)
							region.Order = order
							order++
							out.Actions = append(out.Actions, region)
						}
					} else if err := enc.EncodeToken(xml.CharData(attrs["_dynamic_text"])); err != nil {
						return nil, out, err
					}
				}
			}
		case xml.EndElement:
			current := stack[len(stack)-1]
			if !current.skip {
				if err := enc.EncodeToken(t); err != nil {
					return nil, out, err
				}
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			current := stack[len(stack)-1]
			if current.name == "style" {
				if err := validateStyle(string(t)); err != nil {
					return nil, out, err
				}
				t = xml.CharData(normalizeFontCSS(string(t)))
			}
			if !current.skip && !current.dynamic {
				if err := enc.EncodeToken(t); err != nil {
					return nil, out, err
				}
			}
		default:
			if !parent.skip {
				if err := enc.EncodeToken(tok); err != nil {
					return nil, out, err
				}
			}
		}
	}
	if !rootSeen {
		return nil, out, errors.New("missing SVG root")
	}
	if err := enc.Flush(); err != nil {
		return nil, out, err
	}
	for key := range deps {
		out.Dependencies = append(out.Dependencies, key)
	}
	sort.Strings(out.Dependencies)
	return buf.Bytes(), out, nil
}

func normalizeFontAttrs(attrs []xml.Attr) []xml.Attr {
	for i := range attrs {
		switch attrs[i].Name.Local {
		case "font-family":
			attrs[i].Value = normalizeFontFamily(attrs[i].Value)
		case "style":
			attrs[i].Value = normalizeFontCSS(attrs[i].Value)
		}
	}
	return attrs
}

func normalizeFontFamily(value string) string {
	trimmed := strings.Trim(strings.TrimSpace(value), "\"'")
	switch strings.ToLower(trimmed) {
	case "sans-serif", "sans":
		return "Noto Sans"
	case "serif":
		return "Noto Serif"
	}
	return value
}

func normalizeFontCSS(value string) string {
	re := regexp.MustCompile(`(?i)(font-family\s*:\s*)(["']?)(sans-serif|sans|serif)(["']?)`)
	return re.ReplaceAllStringFunc(value, func(match string) string {
		parts := strings.SplitN(match, ":", 2)
		return parts[0] + ":" + normalizeFontFamily(strings.TrimSpace(parts[1]))
	})
}

func forbiddenElement(name string) bool {
	switch name {
	case "script", "foreignobject", "animate", "animatemotion", "animatetransform", "set", "audio", "video", "iframe":
		return true
	}
	return false
}

func validateAttrs(attrs []xml.Attr) error {
	for _, a := range attrs {
		name, value := strings.ToLower(a.Name.Local), strings.TrimSpace(a.Value)
		if strings.HasPrefix(name, "on") {
			return fmt.Errorf("SVG event attribute %q is not allowed", name)
		}
		low := strings.ToLower(value)
		if name == "href" && value != "" && !strings.HasPrefix(low, "#") && !allowedDataURI(low) {
			return errors.New("external SVG resources are not allowed")
		}
		if (name == "style" || name == "href") && (strings.Contains(low, "http:") || strings.Contains(low, "https:") || strings.Contains(low, "file:") || strings.Contains(low, "@import")) {
			return errors.New("external SVG resources are not allowed")
		}
		if name == "style" && strings.Contains(low, "url(") {
			for _, target := range styleURLs(low) {
				if !strings.HasPrefix(target, "#") && !allowedDataURI(target) {
					return errors.New("external SVG resources are not allowed")
				}
			}
		}
	}
	return nil
}

func allowedDataURI(value string) bool {
	for _, prefix := range []string{"data:image/png;", "data:image/jpeg;", "data:font/ttf;", "data:font/otf;", "data:font/woff;", "data:font/woff2;", "data:application/font-woff;"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func validateStyle(style string) error {
	low := strings.ToLower(style)
	if strings.Contains(low, "@import") || strings.Contains(low, "http:") || strings.Contains(low, "https:") || strings.Contains(low, "file:") {
		return errors.New("external SVG resources are not allowed")
	}
	for _, target := range styleURLs(low) {
		if !strings.HasPrefix(target, "#") && !allowedDataURI(target) {
			return errors.New("external SVG resources are not allowed")
		}
	}
	return nil
}

func styleURLs(style string) []string {
	var out []string
	for {
		start := strings.Index(style, "url(")
		if start < 0 {
			return out
		}
		style = style[start+4:]
		end := strings.IndexByte(style, ')')
		if end < 0 {
			return append(out, "invalid")
		}
		out = append(out, strings.Trim(strings.TrimSpace(style[:end]), "\"'"))
		style = style[end+1:]
	}
}

func attrsMap(attrs []xml.Attr) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		m[a.Name.Local] = a.Value
	}
	return m
}

func stripDataAttrs(attrs []xml.Attr) []xml.Attr {
	out := attrs[:0]
	for _, a := range attrs {
		if !strings.HasPrefix(a.Name.Local, "data-") {
			out = append(out, a)
		}
	}
	return out
}

func setAttr(attrs []xml.Attr, name, value string) []xml.Attr {
	for i := range attrs {
		if attrs[i].Name.Local == name {
			attrs[i].Value = value
			return attrs
		}
	}
	return append(attrs, xml.Attr{Name: xml.Name{Local: name}, Value: value})
}

func substitute(input string, values Values) (string, []string, error) {
	var used []string
	var missing string
	out := variablePattern.ReplaceAllStringFunc(input, func(token string) string {
		key := variablePattern.FindStringSubmatch(token)[1]
		value, ok := values[key]
		if !ok {
			missing = key
			return token
		}
		used = append(used, key)
		return value
	})
	if missing != "" {
		return "", nil, fmt.Errorf("unknown dynamic value %q", missing)
	}
	if strings.Contains(out, "${") {
		return "", nil, errors.New("invalid dynamic value expression")
	}
	return out, used, nil
}

func elementRect(name string, attrs map[string]string) (Rect, error) {
	if hit := attrs["data-hitbox"]; hit != "" {
		v, err := numbers(hit, 4)
		if err != nil || v[2] <= 0 || v[3] <= 0 {
			return Rect{}, errors.New("data-hitbox must contain x y width height")
		}
		return Rect{X: int(v[0]), Y: int(v[1]), Width: int(v[2]), Height: int(v[3])}, nil
	}
	get := func(key string) float64 {
		v, _ := strconv.ParseFloat(strings.TrimSuffix(attrs[key], "px"), 64)
		return v
	}
	switch name {
	case "rect", "image":
		return Rect{X: int(get("x")), Y: int(get("y")), Width: int(get("width")), Height: int(get("height"))}, nil
	case "circle":
		r := get("r")
		return Rect{X: int(get("cx") - r), Y: int(get("cy") - r), Width: int(2 * r), Height: int(2 * r)}, nil
	case "ellipse":
		rx, ry := get("rx"), get("ry")
		return Rect{X: int(get("cx") - rx), Y: int(get("cy") - ry), Width: int(2 * rx), Height: int(2 * ry)}, nil
	default:
		return Rect{}, fmt.Errorf("%s with data-action/data-region requires data-hitbox", name)
	}
}

func transformRect(r Rect, m matrix, vx, vy, vw, vh float64, width, height int) Rect {
	points := [][2]float64{{float64(r.X), float64(r.Y)}, {float64(r.X + r.Width), float64(r.Y)}, {float64(r.X), float64(r.Y + r.Height)}, {float64(r.X + r.Width), float64(r.Y + r.Height)}}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, point := range points {
		x, y := m.point(point[0], point[1])
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	scale := math.Min(float64(width)/vw, float64(height)/vh)
	ox, oy := (float64(width)-vw*scale)/2-vx*scale, (float64(height)-vh*scale)/2-vy*scale
	r.X, r.Y = int(math.Floor(minX*scale+ox)), int(math.Floor(minY*scale+oy))
	r.Width, r.Height = int(math.Ceil((maxX-minX)*scale)), int(math.Ceil((maxY-minY)*scale))
	return r
}

func numbers(s string, count int) ([]float64, error) {
	parts := strings.Fields(strings.NewReplacer(",", " ").Replace(s))
	if len(parts) != count {
		return nil, errors.New("wrong number count")
	}
	out := make([]float64, count)
	for i := range parts {
		v, err := strconv.ParseFloat(parts[i], 64)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func parseTransform(s string) (matrix, error) {
	m := identity
	re := regexp.MustCompile(`([a-zA-Z]+)\s*\(([^)]*)\)`)
	matches := re.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return m, errors.New("invalid SVG transform")
	}
	for _, match := range matches {
		parts := strings.Fields(strings.NewReplacer(",", " ").Replace(match[2]))
		vals := make([]float64, len(parts))
		for i := range parts {
			v, err := strconv.ParseFloat(parts[i], 64)
			if err != nil {
				return m, errors.New("invalid SVG transform")
			}
			vals[i] = v
		}
		var n matrix
		switch strings.ToLower(match[1]) {
		case "translate":
			if len(vals) < 1 || len(vals) > 2 {
				return m, errors.New("invalid translate")
			}
			y := 0.0
			if len(vals) == 2 {
				y = vals[1]
			}
			n = matrix{1, 0, 0, 1, vals[0], y}
		case "scale":
			if len(vals) < 1 || len(vals) > 2 {
				return m, errors.New("invalid scale")
			}
			y := vals[0]
			if len(vals) == 2 {
				y = vals[1]
			}
			n = matrix{vals[0], 0, 0, y, 0, 0}
		case "matrix":
			if len(vals) != 6 {
				return m, errors.New("invalid matrix")
			}
			copy(n[:], vals)
		default:
			return m, fmt.Errorf("unsupported action-region transform %q; use data-hitbox", match[1])
		}
		m = m.mul(n)
	}
	return m, nil
}
