package time

import (
	"fmt"
	"strconv"
	"time"
)

const (
	SecMs  = 1000
	MinMs  = 60 * SecMs
	HourMs = 60 * MinMs
	DayMs  = 24 * HourMs
	WeekMs = 7 * DayMs
)

const TimeFormat = "2006-01-02T15:04:05.000Z"

var (
	timezoneOffset = int64(0)         // ms 与零时区的偏移毫秒数
	timeOffset     = time.Duration(0) // 时间偏移
	location       = time.UTC         // 默认UTC时区
)

// SetTimezone 设置时区
func SetTimezone(offsetSeconds int64) {
	name := fmt.Sprintf("UTC%+d", offsetSeconds/3600)
	location = time.FixedZone(name, int(offsetSeconds))
	timezoneOffset = offsetSeconds * 1e3
}

func GetLocation() *time.Location {
	return location
}

// GetTimeZoneOffset 获取时区偏移量
func GetTimeZoneOffset() int64 {
	return timezoneOffset
}

// SetTimeOffset 设置时间偏移量
func SetTimeOffset(newOffset time.Duration) {
	timeOffset = newOffset
}

// GetTimeOffset 获取时间偏移量
func GetTimeOffset() time.Duration {
	return timeOffset
}

// Now 获取当前时间
func Now() time.Time {
	now := time.Now()
	if timeOffset != 0 {
		now = now.Add(timeOffset)
	}
	return now.In(location)
}

// NowMS 获取当前时间的毫秒时间戳
func NowMs() int64 {
	return Now().UnixMilli()
}

// Time2Ms 系统时间转化为 ms时间戳
func Time2Ms(t time.Time) int64 {
	return t.UnixMilli()
}

// Ms2Time ms时间戳转化为时间
func Ms2Time(ms int64) time.Time {
	return time.UnixMilli(ms).In(location)
}

// Ms2DateValue 从一个毫秒时间戳转换成年月日整型数值
func Ms2DateValue(t int64) int64 {
	str := Ms2Time(t).Format("20060102")
	val, _ := strconv.ParseInt(str, 10, 64)
	return val
}

// GetWeekday 根据时间戳算出指定时区的周几，0是周天
func GetWeekday(nowMs int64) int64 {
	ws := (nowMs + timezoneOffset) % WeekMs
	d := ws / DayMs
	result := (d + 4) % 7
	return result
}

// IsNewDayReset 是否跨天某一小时, before\after为UTC时间戳ms, hour(0-23)
func IsNewDayReset(before, after int64, resetHour int64) bool {
	before += timezoneOffset
	after += timezoneOffset
	return ((before - (resetHour * HourMs)) / DayMs) != ((after - (resetHour * HourMs)) / DayMs)
}

// GetNextDayResetTime 获取下次重置时间戳, nowMs为UTC时间戳
func GetNextDayResetTime(nowMs int64, resetHour int64) int64 {
	thisDayResetTime := nowMs - ((nowMs + timezoneOffset) % DayMs) + (resetHour * HourMs)
	if thisDayResetTime > nowMs {
		return thisDayResetTime
	} else {
		return thisDayResetTime + DayMs
	}
}

// GetLastResetTime 获取当前时间的上次跨天时间戳，nowMs为时间戳ms
func GetLastResetTime(nowMs int64, resetHour int64) int64 {
	next := GetNextDayResetTime(nowMs, resetHour)
	return next - DayMs
}

// IsWeekReset 是否跨周before, after为UTC时间戳ms
func IsWeekReset(before, after int64, resetHour, resetWeekDay int64) bool {
	weekResetTime := GetNextWeekResetTime(before, resetHour, resetWeekDay)
	return after >= weekResetTime
}

// GetNextWeekResetTime 获取下周重置时间（时刻）, nowMs为UTC时间戳ms, 周天0和7都合法
func GetNextWeekResetTime(nowMs int64, resetHour, resetWeekDay int64) int64 {
	nowWeekDay := GetWeekday(nowMs)
	diff := (resetWeekDay - nowWeekDay) * DayMs
	thisWeekResetTime := nowMs - ((nowMs + timezoneOffset) % DayMs) + diff + (resetHour * HourMs)
	if thisWeekResetTime > nowMs {
		return thisWeekResetTime
	} else {
		return thisWeekResetTime + WeekMs
	}
}

// GetLastWeekResetTime 获取当前时间的上次跨周时间戳，nowMs为时间戳ms
func GetLastWeekResetTime(nowMs int64, resetHour, resetWeekDay int64) int64 {
	next := GetNextWeekResetTime(nowMs, resetHour, resetWeekDay)
	return next - WeekMs
}
