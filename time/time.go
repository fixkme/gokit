package time

import (
	"strconv"
	"time"
)

const (
	SecMs  = 1000
	MinMs  = 60 * SecMs
	HourMs = 60 * MinMs
	DayMs  = 24 * HourMs
	WeekMs = 7 * DayMs
	YearMs = 365 * DayMs
)

const TimeFormat = "2006-01-02T15:04:05.000Z"

var (
	timezoneOffset = int64(0)         // ms 与零时区的偏移毫秒数
	timeOffset     = time.Duration(0) // 时间偏移
	location       = time.UTC
)

// SetTimezone 设置时区
func SetTimezone(offset int64) {
	location = time.FixedZone("UTC", int(offset))
	timezoneOffset = offset * 1e3
}

func GetLocation() *time.Location {
	return location
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
	if timeOffset > 0 {
		now = now.Add(timeOffset)
	}
	return now.In(location)
}

// NowMS 获取当前时间的毫秒时间戳
func NowMS() int64 {
	now := Now()
	return Time2Ms(now)
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

// IsNewDayReset 是否跨天某一小时, before\after为UTC时间戳ms, hour(0-23)
func IsNewDayReset(before, after int64) bool {
	before += timezoneOffset
	after += timezoneOffset
	//((before - (resetHour * HourMs)) / DayMs) != ((after - (resetHour * HourMs)) / DayMs)
	return (before / DayMs) != (after / DayMs)
}

func IsNewHourReset(before, after int64) bool {
	before += timezoneOffset
	after += timezoneOffset
	return (before / HourMs) != (after / HourMs)
}

// GetNextDayResetTime 获取下次重置时间戳, nowMs为UTC时间戳
func GetNextDayResetTime(nowMs int64) int64 {
	thisDayResetTime := nowMs - ((nowMs + timezoneOffset) % DayMs) // + (int64(resetHour) * HourMs)
	if thisDayResetTime > nowMs {
		return thisDayResetTime
	} else {
		return thisDayResetTime + DayMs
	}
}

// GetLatestResetTime 获取当前时间的最近跨天时间戳，nowMs为时间戳ms
func GetLatestResetTime(nowMs int64) int64 {
	next := GetNextDayResetTime(nowMs)
	return next - DayMs
}

// IsWeekReset 是否跨周before, after为UTC时间戳ms
func IsWeekReset(before, after int64, resetWeekDay int) bool {
	weekResetTime := GetNextWeekResetTime(before, resetWeekDay)
	return after >= weekResetTime
}

// GetNextWeekResetTime 获取下周重置时间（时刻）, nowMs为UTC时间戳ms, 周天0和7都合法
func GetNextWeekResetTime(nowMs int64, resetWeekDay int) int64 {
	nowWeekDay := int(GetWeekday(nowMs))
	diff := int64(resetWeekDay-nowWeekDay) * DayMs
	thisWeekResetTime := nowMs - ((nowMs + timezoneOffset) % DayMs) + diff //+ (resetHour * HourMs)
	if thisWeekResetTime > nowMs {
		return thisWeekResetTime
	} else {
		return thisWeekResetTime + WeekMs
	}
}

// GetLatestWeekResetTime 获取当前时间的最近跨周时间戳，nowMs为时间戳ms
func GetLatestWeekResetTime(nowMs int64, resetWeekDay int) int64 {
	next := GetNextWeekResetTime(nowMs, resetWeekDay)
	return next - WeekMs
}

// GetWeekday 根据时间戳算出指定时区的周几，0是周天
func GetWeekday(nowMs int64) int64 {
	ws := (nowMs + timezoneOffset) % WeekMs
	d := ws / DayMs
	result := (d + 4) % 7
	return result
}

// GetTimeZoneOffset 获取时区偏移量
func GetTimeZoneOffset() int64 {
	return timezoneOffset
}
