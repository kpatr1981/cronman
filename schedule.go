package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type schedule struct {
	sets    [5][]bool
	domStar bool
	dowStar bool
	reboot  bool
}

func compileSchedule(s string) (*schedule, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "@") {
		exp, ok := specialSchedules[strings.ToLower(s)]
		if !ok {
			return nil, fmt.Errorf("unknown special schedule %q", s)
		}
		if exp == "" {
			return &schedule{reboot: true}, nil
		}
		s = exp
	}
	f := wsRe.Split(s, -1)
	if len(f) != 5 {
		return nil, fmt.Errorf("need 5 fields")
	}
	sc := &schedule{domStar: strings.HasPrefix(f[2], "*"), dowStar: strings.HasPrefix(f[4], "*")}
	for i := range f {
		set, err := expandField(i, f[i])
		if err != nil {
			return nil, err
		}
		sc.sets[i] = set
	}
	return sc, nil
}

func (s *schedule) dayMatches(t time.Time) bool {
	dom := s.sets[2][t.Day()]
	dow := s.sets[4][int(t.Weekday())]
	// Vixie cron: if both day fields are restricted, either may match.
	if !s.domStar && !s.dowStar {
		return dom || dow
	}
	return dom && dow
}

// NextRuns returns up to n run times strictly after from.
func NextRuns(sched string, from time.Time, n int) ([]time.Time, error) {
	s, err := compileSchedule(sched)
	if err != nil || s.reboot {
		return nil, err
	}
	var out []time.Time
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := from.AddDate(5, 0, 0)
	for len(out) < n && t.Before(limit) {
		switch {
		case !s.sets[3][int(t.Month())]:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
		case !s.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
		case !s.sets[1][t.Hour()]:
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
		case !s.sets[0][t.Minute()]:
			t = t.Add(time.Minute)
		default:
			out = append(out, t)
			t = t.Add(time.Minute)
		}
	}
	return out, nil
}

var dowNames = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

// Describe gives a short human description of common schedules.
func Describe(sched string) string {
	sched = strings.TrimSpace(sched)
	switch strings.ToLower(sched) {
	case "@reboot":
		return "at boot"
	case "@hourly":
		return "every hour"
	case "@daily", "@midnight":
		return "daily at 00:00"
	case "@weekly":
		return "weekly, Sun 00:00"
	case "@monthly":
		return "monthly, 1st 00:00"
	case "@yearly", "@annually":
		return "yearly, Jan 1 00:00"
	}
	f := wsRe.Split(sched, -1)
	if len(f) != 5 {
		return ""
	}
	min, hour, dom, mon, dow := f[0], f[1], f[2], f[3], f[4]
	rest := dom == "*" && mon == "*" && dow == "*"
	isNum := func(s string) bool { _, err := strconv.Atoi(s); return err == nil }
	if hour == "*" && rest {
		switch {
		case min == "*":
			return "every minute"
		case strings.HasPrefix(min, "*/"):
			return "every " + min[2:] + " minutes"
		case isNum(min):
			return "hourly at :" + pad2(min)
		}
		return ""
	}
	if !isNum(min) {
		return ""
	}
	var when string
	switch {
	case isNum(hour):
		h, _ := strconv.Atoi(hour)
		m, _ := strconv.Atoi(min)
		when = fmt.Sprintf("at %02d:%02d", h, m)
	case strings.HasPrefix(hour, "*/"):
		when = "every " + hour[2:] + " hours at :" + pad2(min)
	default:
		return ""
	}
	switch {
	case rest:
		return "daily " + when
	case dom == "*" && mon == "*":
		return describeDow(dow) + " " + when
	case isNum(dom) && dow == "*" && mon == "*":
		return "monthly on day " + dom + " " + when
	}
	return ""
}

func describeDow(dow string) string {
	conv := func(s string) string {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 && n <= 7 {
			return dowNames[n]
		}
		if len(s) >= 3 {
			return strings.ToUpper(s[:1]) + strings.ToLower(s[1:3])
		}
		return s
	}
	var parts []string
	for _, item := range strings.Split(dow, ",") {
		if a, b, ok := strings.Cut(item, "-"); ok {
			parts = append(parts, conv(a)+"-"+conv(b))
		} else {
			parts = append(parts, conv(item))
		}
	}
	return "on " + strings.Join(parts, ",")
}

func pad2(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}
