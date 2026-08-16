use chrono::{DateTime, FixedOffset};
use extism_pdk::{FnResult, HttpRequest, http, plugin_fn};
use serde::Deserialize;

const USER_AGENT: &str = "eink-server/1.0 (+https://github.com/gyger/JoanTablet)";

#[derive(Deserialize)]
struct Input {
    now: String,
    locale: String,
    viewport: Viewport,
    config: std::collections::HashMap<String, String>,
}

#[derive(Deserialize)]
struct Viewport {
    width: i32,
    height: i32,
}

#[derive(Deserialize)]
struct Response {
    #[serde(rename = "stopTimes")]
    stop_times: Vec<StopTime>,
    place: ResponsePlace,
}

#[derive(Deserialize)]
struct ResponsePlace {
    #[serde(default)]
    name: String,
}

#[derive(Deserialize)]
struct StopTime {
    place: Place,
    #[serde(default)]
    mode: String,
    #[serde(default, rename = "realTime")]
    real_time: bool,
    #[serde(default)]
    headsign: String,
    #[serde(default, rename = "displayName")]
    display_name: String,
    #[serde(default, rename = "routeShortName")]
    route_short_name: String,
    #[serde(default)]
    cancelled: bool,
    #[serde(default, rename = "tripCancelled")]
    trip_cancelled: bool,
}

#[derive(Deserialize)]
struct Place {
    #[serde(default)]
    departure: String,
    #[serde(default, rename = "scheduledDeparture")]
    scheduled_departure: String,
    #[serde(default)]
    track: String,
    #[serde(default, rename = "scheduledTrack")]
    scheduled_track: String,
    #[serde(default)]
    cancelled: bool,
    #[serde(default)]
    modes: Vec<String>,
}

struct Departure {
    route: String,
    destination: String,
    platform: String,
    effective: i64,
    scheduled: i64,
    realtime: bool,
    cancelled: bool,
}

#[plugin_fn]
pub fn render(input: String) -> FnResult<String> {
    let input: Input = serde_json::from_str(&input)?;
    let stop = config(&input, "stop-id").trim();
    if stop.is_empty() {
        return Ok(placeholder(&input));
    }
    let rows = config(&input, "rows")
        .parse::<usize>()
        .unwrap_or(5)
        .clamp(1, 20);
    let language = input.locale.split('-').next().unwrap_or("de");
    let url = format!(
        "https://api.transitous.org/api/v6/stoptimes?stopId={}&n={rows}&arriveBy=false&language={}",
        urlencoding::encode(stop),
        urlencoding::encode(language)
    );
    let request = HttpRequest::new(&url)
        .with_method("GET")
        .with_header("User-Agent", USER_AGENT);
    let response = http::request::<()>(&request, None)?;
    if !(200..300).contains(&response.status_code()) {
        return Err(
            extism_pdk::Error::msg(format!("Transitous HTTP {}", response.status_code())).into(),
        );
    }
    let decoded: Response = serde_json::from_slice(&response.body())?;
    let station_name = decoded.place.name.clone();
    let wanted = format!(
        ",{},",
        config(&input, "modes").replace(' ', "").to_uppercase()
    );
    let mut departures = Vec::new();
    for item in decoded.stop_times {
        let scheduled = timestamp(&item.place.scheduled_departure).unwrap_or(0);
        let effective = timestamp(&item.place.departure).unwrap_or(scheduled);
        if effective == 0 {
            continue;
        }
        let mode_name = if item.mode.is_empty() {
            item.place.modes.first().cloned().unwrap_or_default()
        } else {
            item.mode
        };
        if wanted != ",," && !wanted.contains(&format!(",{},", mode_name.to_uppercase())) {
            continue;
        }
        departures.push(Departure {
            route: first(item.display_name, item.route_short_name),
            destination: item.headsign,
            platform: first(item.place.track, item.place.scheduled_track),
            effective,
            scheduled,
            realtime: item.real_time,
            cancelled: item.cancelled || item.trip_cancelled || item.place.cancelled,
        });
    }
    departures.sort_by_key(|d| d.effective);
    departures.truncate(rows);
    let now = DateTime::parse_from_rfc3339(&input.now)?;
    Ok(svg(&input, now, &station_name, &departures))
}

fn timestamp(value: &str) -> Option<i64> {
    DateTime::parse_from_rfc3339(value)
        .ok()
        .map(|v| v.timestamp())
}
fn config<'a>(input: &'a Input, key: &str) -> &'a str {
    input.config.get(key).map(String::as_str).unwrap_or("")
}
fn first(a: String, b: String) -> String {
    if a.is_empty() { b } else { a }
}
fn escape(value: &str) -> String {
    value
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}

fn placeholder(input: &Input) -> String {
    format!(
        r#"<g><text x="0" y="48" font-family="Noto Sans" font-size="34" font-weight="600">{}</text><text x="0" y="100" font-family="Noto Sans" font-size="25">Configure stop</text><text x="0" y="{}" font-family="Noto Sans" font-size="15">Data: Transitous</text></g>"#,
        escape(if config(input, "title").is_empty() {
            "Departures"
        } else {
            config(input, "title")
        }),
        input.viewport.height - 8
    )
}

fn svg(
    input: &Input,
    now: DateTime<FixedOffset>,
    station_name: &str,
    departures: &[Departure],
) -> String {
    let title = if config(input, "title").is_empty() {
        "Departures"
    } else {
        config(input, "title")
    };
    let mut out = format!(
        r#"<g font-family="Noto Sans" fill="black"><text x="0" y="38" font-size="32" font-weight="600">{}</text>"#,
        escape(title)
    );
    if !station_name.is_empty() {
        out.push_str(&format!(
            r#"<text x="0" y="68" font-size="21">{}</text>"#,
            escape(&truncate(station_name, 48))
        ));
    }
    let mut y = 112;
    if departures.is_empty() {
        out.push_str(&format!(
            r#"<text x="0" y="{y}" font-size="24">No departures</text>"#
        ));
    }
    for d in departures {
        let mins = ((d.effective - now.timestamp()) / 60).max(0);
        let label = if mins <= 10 {
            format!("{mins} min")
        } else {
            wall_time_ascii(d.effective, now.offset().local_minus_utc())
        };
        let delay = (d.effective - d.scheduled) / 60;
        let status = if d.realtime && delay != 0 {
            format!("RT {delay:+} min")
        } else if d.realtime {
            "RT".into()
        } else {
            String::new()
        };
        let cancelled_style = if d.cancelled {
            r#" text-decoration="line-through""#
        } else {
            ""
        };
        out.push_str(&format!(r#"<text x="0" y="{y}" font-size="28" font-weight="700"{cancelled_style}>{}</text><text x="105" y="{y}" font-size="25">{}</text>"#,escape(&d.route),escape(&truncate(&d.destination, 27))));
        out.push_str(&format!(r#"<text x="{}" y="{y}" text-anchor="end" font-size="25" font-weight="600"{cancelled_style}>{}</text>"#,input.viewport.width,escape(&label)));
        let platform = if d.platform.is_empty() {
            String::new()
        } else {
            format!("Platform {}", d.platform)
        };
        let sub = format!("{}  {}", platform, status).trim().to_string();
        if !sub.is_empty() {
            out.push_str(&format!(
                r#"<text x="105" y="{}" font-size="17">{}</text>"#,
                y + 25,
                escape(&sub)
            ));
        }
        y += 100;
    }
    out.push_str(&format!(
        r#"<text x="0" y="{}" font-size="15">Data: Transitous · transitous.org</text></g>"#,
        input.viewport.height - 8
    ));
    out
}

fn truncate(value: &str, max: usize) -> String {
    if value.chars().count() <= max {
        return value.to_string();
    }
    let mut out: String = value.chars().take(max.saturating_sub(1)).collect();
    out.push('…');
    out
}

fn wall_time_ascii(timestamp: i64, utc_offset_seconds: i32) -> String {
    let seconds = (timestamp + i64::from(utc_offset_seconds)).rem_euclid(24 * 60 * 60);
    let hour = (seconds / (60 * 60)) as u8;
    let minute = ((seconds / 60) % 60) as u8;
    let bytes = [
        b'0' + hour / 10,
        b'0' + hour % 10,
        b':',
        b'0' + minute / 10,
        b'0' + minute % 10,
    ];
    String::from_utf8(bytes.to_vec()).expect("wall-time bytes are ASCII")
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn escapes_svg() {
        assert_eq!(escape("<&\"'"), "&lt;&amp;&quot;&apos;");
    }
    #[test]
    fn truncates_long_directions() {
        assert_eq!(truncate("abcdefghijklmnopqrstuvwxyzAB", 8), "abcdefg…");
    }
    #[test]
    fn effective_departure_drives_countdown() {
        let scheduled = timestamp("2026-08-14T18:00:00+02:00").unwrap();
        let effective = timestamp("2026-08-14T18:07:00+02:00").unwrap();
        let now = timestamp("2026-08-14T17:57:00+02:00").unwrap();
        assert_eq!((effective - now) / 60, 10);
        assert_eq!((effective - scheduled) / 60, 7);
    }
    #[test]
    fn cancellation_strikes_route_and_time_but_not_destination() {
        let input = Input {
            now: "2026-08-14T18:00:00+02:00".into(),
            locale: "de-DE".into(),
            viewport: Viewport {
                width: 610,
                height: 675,
            },
            config: std::collections::HashMap::new(),
        };
        let departure = Departure {
            route: "102".into(),
            destination: "Dudweiler".into(),
            platform: String::new(),
            effective: timestamp("2026-08-14T18:05:00+02:00").unwrap(),
            scheduled: timestamp("2026-08-14T18:05:00+02:00").unwrap(),
            realtime: true,
            cancelled: true,
        };
        let rendered = svg(
            &input,
            DateTime::parse_from_rfc3339(&input.now).unwrap(),
            "Universität Mensa, Saarbrücken",
            &[departure],
        );
        assert_eq!(
            rendered
                .matches(r#"text-decoration="line-through""#)
                .count(),
            2
        );
        assert!(rendered.contains(r#">Dudweiler</text>"#));
        assert!(!rendered.contains("CANCELLED"));
    }

    #[test]
    fn wall_time_uses_explicit_ascii_digits() {
        let input = Input {
            now: "2026-08-14T22:00:00+02:00".into(),
            locale: "de-DE".into(),
            viewport: Viewport {
                width: 610,
                height: 675,
            },
            config: std::collections::HashMap::new(),
        };
        let departure = Departure {
            route: "102".into(),
            destination: "Dudweiler".into(),
            platform: String::new(),
            effective: timestamp("2026-08-14T23:46:00+02:00").unwrap(),
            scheduled: timestamp("2026-08-14T23:46:00+02:00").unwrap(),
            realtime: true,
            cancelled: false,
        };
        let rendered = svg(
            &input,
            DateTime::parse_from_rfc3339(&input.now).unwrap(),
            "Prinzenweiher/DJH, Saarbrücken",
            &[departure],
        );
        assert!(rendered.contains(">23:46</text>"));
        assert_eq!(wall_time_ascii(departure_time(), 2 * 60 * 60), "23:46");
    }

    fn departure_time() -> i64 {
        timestamp("2026-08-14T23:46:00+02:00").unwrap()
    }
}
