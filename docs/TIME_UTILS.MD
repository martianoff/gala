# Time Utils

The `time_utils` package provides immutable types for working with time in GALA. It offers `Duration` for representing lengths of time and `Instant` for representing specific moments in time.

## Import

```gala
import . "martianoff/gala/time_utils"
```

## Duration

`Duration` represents a length of time stored as nanoseconds. All operations return new Duration values (immutable).

### Creating Durations

```gala
// From various time units
val ns = Nanoseconds(1000)
val us = Microseconds(500)
val ms = Milliseconds(100)
val sec = Seconds(30)
val min = Minutes(5)
val hr = Hours(2)
val day = Days(1)

// Zero duration
val zero = ZeroDuration()

// From Go time.Duration
val goDur = FromGoDuration(time.Second * 5)

// Between two instants
val elapsed = Between(start, end)
```

### Duration Operations

```gala
val d1 = Seconds(30)
val d2 = Seconds(15)

// Arithmetic
val sum = d1.Plus(d2)       // 45 seconds
val diff = d1.Minus(d2)     // 15 seconds
val doubled = d1.Multiply(2) // 60 seconds
val halved = d1.Divide(2)   // 15 seconds

// Sign operations
val neg = d1.Negate()       // -30 seconds
val abs = neg.Abs()         // 30 seconds

// Predicates
d1.IsZero()                 // false
d1.IsPositive()             // true
d1.IsNegative()             // false

// Comparison
d1.Compare(d2)              // 1 (d1 > d2)
d1.Equals(d2)               // false
```

### Duration Conversions

```gala
val d = Hours(2).Plus(Minutes(30))

d.ToNanos()                 // nanoseconds
d.ToMicros()                // microseconds
d.ToMillis()                // milliseconds
d.ToSeconds()               // 9000
d.ToMinutes()               // 150
d.ToHours()                 // 2
d.ToDays()                  // 0

d.ToGoDuration()            // Go time.Duration
d.String()                  // "2h30m0s"
```

## Instant

`Instant` represents a specific moment in time stored as nanoseconds since Unix epoch. All operations return new Instant values (immutable).

### Creating Instants

```gala
// Current time
val now = Now()

// From Unix timestamps
val fromSecs = FromUnixSeconds(1704067200)
val fromMillis = FromUnixMillis(1704067200000)
val fromNanos = FromUnixNanos(1704067200000000000)

// Unix epoch (January 1, 1970 UTC)
val epoch = UnixEpoch()

// From Go time.Time
val fromGo = FromGoTime(time.Now())

// Parsing strings
val parsed = ParseISO("2024-01-15T10:30:00Z")           // Option[Instant]
val rfc3339 = ParseRFC3339("2024-01-15T10:30:00Z")     // Option[Instant]
val nano = ParseRFC3339Nano("2024-01-15T10:30:00.123456789Z")
val custom = Parse(time.Kitchen, "3:04PM")             // Option[Instant]
```

### Instant Operations

```gala
val i1 = FromUnixSeconds(1000)
val i2 = FromUnixSeconds(2000)

// Add/subtract durations
val later = i1.Plus(Hours(1))
val earlier = i1.Minus(Minutes(30))

// Calculate durations between instants
val until = i1.Until(i2)    // Duration from i1 to i2
val since = i2.Since(i1)    // Duration from i1 to i2

// Comparison
i1.IsBefore(i2)             // true
i1.IsAfter(i2)              // false
i1.Equals(i2)               // false
i1.Compare(i2)              // -1

// Min/Max
i1.Min(i2)                  // i1 (earlier)
i1.Max(i2)                  // i2 (later)

// Relative to now
val elapsed = pastInstant.SinceNow()   // Duration since instant
val remaining = futureInstant.UntilNow() // Duration until instant (negative if past)
```

### Instant Accessors

```gala
val i = ParseISO("2024-06-15T10:30:45Z").Get()

// Unix timestamps
i.UnixSeconds()             // seconds since epoch
i.UnixMillis()              // milliseconds since epoch
i.UnixNanos()               // nanoseconds since epoch

// Date components (UTC)
i.Year()                    // 2024
i.Month()                   // 6
i.Day()                     // 15
i.Hour()                    // 10
i.Minute()                  // 30
i.Second()                  // 45
i.Nanosecond()              // 0
i.Weekday()                 // 6 (Saturday, 0=Sunday)
i.YearDay()                 // 167

// Go interop
i.ToGoTime()                // time.Time in UTC
```

### Formatting

```gala
val i = Now()

i.FormatISO()               // "2024-06-15T10:30:45Z"
i.FormatRFC3339()           // "2024-06-15T10:30:45Z"
i.FormatRFC3339Nano()       // "2024-06-15T10:30:45.123456789Z"
i.Format(time.Kitchen)      // "10:30AM"
i.String()                  // ISO 8601 format
```

### Truncation

```gala
val i = ParseRFC3339Nano("2024-06-15T10:30:45.123456789Z").Get()

i.TruncateToSecond()        // 2024-06-15T10:30:45Z
i.TruncateToMinute()        // 2024-06-15T10:30:00Z
i.TruncateToHour()          // 2024-06-15T10:00:00Z
i.TruncateToDay()           // 2024-06-15T00:00:00Z
```

### Timezone Operations

```gala
val i = Now()

// Get time in specific timezone
val inNY = i.InTimezone("America/New_York")  // Option[time.Time]
val inTokyo = i.InTimezone("Asia/Tokyo")     // Option[time.Time]

inNY match {
    case Some(t) => fmt.Println(t.Format(time.RFC3339))
    case _ => fmt.Println("Invalid timezone")
}
```

## Timer Utilities

```gala
// Sleep for a duration
Sleep(Seconds(1))

// Get instant after duration from now
val deadline = After(Minutes(5))
```

## Pattern Matching Extractors

The package provides extractors for pattern matching on Instant values.

### InstantComponents

Extract year, month, day, hour, minute, second:

```gala
val i = Now()

i match {
    case InstantComponents(year, month, day, hour, minute, second) =>
        fmt.Printf("%d-%02d-%02d %02d:%02d:%02d\n", year, month, day, hour, minute, second)
    case _ => fmt.Println("Unknown")
}
```

### DateOnly

Extract just the date components:

```gala
val i = Now()

i match {
    case DateOnly(year, month, day) =>
        fmt.Printf("%d-%02d-%02d\n", year, month, day)
    case _ => fmt.Println("Unknown")
}
```

### TimeOnly

Extract just the time components:

```gala
val i = Now()

i match {
    case TimeOnly(hour, minute, second) =>
        fmt.Printf("%02d:%02d:%02d\n", hour, minute, second)
    case _ => fmt.Println("Unknown")
}
```

## Complete Example

```gala
package main

import (
    "fmt"
    . "martianoff/gala/time_utils"
)

func main() {
    // Measure execution time
    val start = Now()

    // Do some work
    Sleep(Milliseconds(100))

    val elapsed = start.SinceNow().Abs()
    fmt.Printf("Execution took %s\n", elapsed.String())

    // Parse and format dates
    val parsed = ParseISO("2024-12-25T00:00:00Z")
    parsed match {
        case Some(christmas) =>
            val daysUntil = Now().Until(christmas).ToDays()
            fmt.Printf("Days until Christmas: %d\n", daysUntil)
        case _ =>
            fmt.Println("Failed to parse date")
    }

    // Work with durations
    val workday = Hours(8)
    val lunch = Minutes(30)
    val actualWork = workday.Minus(lunch)
    fmt.Printf("Actual work time: %s\n", actualWork.String())

    // Extract components
    val now = Now()
    now match {
        case DateOnly(y, m, d) =>
            fmt.Printf("Today is %d-%02d-%02d\n", y, m, d)
        case _ =>
            fmt.Println("Unknown")
    }
}
```

## API Reference

### Duration Methods

| Method | Return Type | Description |
|--------|-------------|-------------|
| `Plus(other)` | `Duration` | Add two durations |
| `Minus(other)` | `Duration` | Subtract duration |
| `Multiply(factor)` | `Duration` | Multiply by factor |
| `Divide(divisor)` | `Duration` | Divide by divisor |
| `Abs()` | `Duration` | Absolute value |
| `Negate()` | `Duration` | Negate duration |
| `IsZero()` | `bool` | Check if zero |
| `IsPositive()` | `bool` | Check if positive |
| `IsNegative()` | `bool` | Check if negative |
| `Compare(other)` | `int` | Compare (-1, 0, 1) |
| `Equals(other)` | `bool` | Check equality |
| `ToNanos()` | `int64` | Get nanoseconds |
| `ToMicros()` | `int64` | Get microseconds |
| `ToMillis()` | `int64` | Get milliseconds |
| `ToSeconds()` | `int64` | Get seconds |
| `ToMinutes()` | `int64` | Get minutes |
| `ToHours()` | `int64` | Get hours |
| `ToDays()` | `int64` | Get days |
| `ToGoDuration()` | `time.Duration` | Convert to Go duration |
| `String()` | `string` | Human-readable string |

### Instant Methods

| Method | Return Type | Description |
|--------|-------------|-------------|
| `Plus(d)` | `Instant` | Add duration |
| `Minus(d)` | `Instant` | Subtract duration |
| `Until(other)` | `Duration` | Duration to other instant |
| `Since(other)` | `Duration` | Duration from other instant |
| `IsBefore(other)` | `bool` | Check if before |
| `IsAfter(other)` | `bool` | Check if after |
| `Equals(other)` | `bool` | Check equality |
| `Compare(other)` | `int` | Compare (-1, 0, 1) |
| `Min(other)` | `Instant` | Earlier instant |
| `Max(other)` | `Instant` | Later instant |
| `UnixSeconds()` | `int64` | Unix seconds |
| `UnixMillis()` | `int64` | Unix milliseconds |
| `UnixNanos()` | `int64` | Unix nanoseconds |
| `ToGoTime()` | `time.Time` | Convert to Go time |
| `Format(layout)` | `string` | Format with layout |
| `FormatISO()` | `string` | ISO 8601 format |
| `FormatRFC3339()` | `string` | RFC 3339 format |
| `FormatRFC3339Nano()` | `string` | RFC 3339 with nanos |
| `String()` | `string` | ISO 8601 format |
| `Year()` | `int` | Get year |
| `Month()` | `int` | Get month (1-12) |
| `Day()` | `int` | Get day (1-31) |
| `Hour()` | `int` | Get hour (0-23) |
| `Minute()` | `int` | Get minute (0-59) |
| `Second()` | `int` | Get second (0-59) |
| `Nanosecond()` | `int` | Get nanosecond |
| `Weekday()` | `int` | Get weekday (0=Sun) |
| `YearDay()` | `int` | Get day of year |
| `InTimezone(tz)` | `Option[time.Time]` | Convert to timezone |
| `TruncateToSecond()` | `Instant` | Truncate to second |
| `TruncateToMinute()` | `Instant` | Truncate to minute |
| `TruncateToHour()` | `Instant` | Truncate to hour |
| `TruncateToDay()` | `Instant` | Truncate to day |
| `SinceNow()` | `Duration` | Duration since now |
| `UntilNow()` | `Duration` | Duration until now |

### Constructor Functions

| Function | Return Type | Description |
|----------|-------------|-------------|
| `Nanoseconds(n)` | `Duration` | Create from nanoseconds |
| `Microseconds(n)` | `Duration` | Create from microseconds |
| `Milliseconds(n)` | `Duration` | Create from milliseconds |
| `Seconds(n)` | `Duration` | Create from seconds |
| `Minutes(n)` | `Duration` | Create from minutes |
| `Hours(n)` | `Duration` | Create from hours |
| `Days(n)` | `Duration` | Create from days |
| `ZeroDuration()` | `Duration` | Zero duration |
| `Between(start, end)` | `Duration` | Duration between instants |
| `FromGoDuration(d)` | `Duration` | From Go duration |
| `Now()` | `Instant` | Current instant |
| `FromUnixSeconds(s)` | `Instant` | From Unix seconds |
| `FromUnixMillis(ms)` | `Instant` | From Unix milliseconds |
| `FromUnixNanos(ns)` | `Instant` | From Unix nanoseconds |
| `FromGoTime(t)` | `Instant` | From Go time.Time |
| `UnixEpoch()` | `Instant` | Unix epoch |
| `Parse(layout, value)` | `Option[Instant]` | Parse with layout |
| `ParseISO(value)` | `Option[Instant]` | Parse ISO 8601 |
| `ParseRFC3339(value)` | `Option[Instant]` | Parse RFC 3339 |
| `ParseRFC3339Nano(value)` | `Option[Instant]` | Parse RFC 3339 with nanos |
| `Sleep(d)` | - | Sleep for duration |
| `After(d)` | `Instant` | Instant after duration |
