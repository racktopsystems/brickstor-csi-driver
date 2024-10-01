// Copyright 2024 RackTop Systems Inc. and/or its affiliates.
// http://www.racktopsystems.com

package rest

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// JsonDuration is a serializable duration.  The serialized (string) form
// uses the format [-][<days>.]<hh>:<mm>:<ss>[.<frac>]
// <days> can be any number of digits.  We will consume 0 or more digits
// of <hh>, but we always emit exactly 2 digits. The <mm> and <ss> must
// always be two digits (and may exceed 60 on input).
// On input, we will consume up to nanosecond precision.
// On output, we round to the nearest microsecond before emitting,
// in order to avoid breaking older code that cannot cope with higher
// precision.
type JsonDuration struct {
	time.Duration
}

var errInvalidJsonDuration = errors.New("invalid duration")

func (d *JsonDuration) marshal(b *bytes.Buffer) {
	dur := d.Duration

	// handle negative durations explicitly, to prevent minus signs from
	// getting inserted at strange places
	if dur < 0 {
		b.WriteByte('-')
		dur = -dur
	}

	dur = dur.Round(time.Microsecond)

	if days := dur / (time.Hour * 24); days > 0 {
		fmt.Fprintf(b, "%d.", days)
		dur %= time.Hour * 24
	}
	fmt.Fprintf(b, "%02d:", dur/time.Hour)
	dur %= time.Hour
	_, _ = fmt.Fprintf(b, "%02d:", dur/time.Minute)
	dur %= time.Minute
	_, _ = fmt.Fprintf(b, "%02d", dur/time.Second)
	dur %= time.Second
	if dur == 0 {
		return
	}
	b.WriteByte('.')
	intvl := time.Second / 10
	// This emits exactly the number of decimal places that are
	// needed, and no more, down to microsecond precision.
	// It is not super efficient.
	for dur > 0 && intvl >= time.Microsecond {
		_, _ = fmt.Fprintf(b, "%d", int64(dur/intvl))
		dur %= intvl
		intvl /= 10
	}
}

// MarshalText provides a byte slice representation in ASCII (unquoted) of
// the value.
func (d *JsonDuration) MarshalText() ([]byte, error) {
	b := &bytes.Buffer{}
	d.marshal(b)
	return b.Bytes(), nil
}

// String implements the GoStringer API.
func (d *JsonDuration) String() string {
	b := &bytes.Buffer{}
	d.marshal(b)
	return b.String()
}

// MarshalJSON complies with the JSON marshaling interface so that it will
// be called by the json package to marshal this data type
func (d *JsonDuration) MarshalJSON() ([]byte, error) {
	b := &bytes.Buffer{}
	b.WriteByte('"')
	d.marshal(b)
	b.WriteByte('"')
	return b.Bytes(), nil
}

// UnmarshalJSON complies with the JSON unmarshaling interface so that it will
// be called by the json package to unmarshal this data type
func (d *JsonDuration) UnmarshalJSON(j []byte) error {
	if len(j) < 2 || j[len(j)-1] != '"' || j[0] != '"' {
		return errInvalidJsonDuration
	}
	return d.parse(j[1 : len(j)-1])
}

// UnmarshalText implements unmarshalling of a bare text duration.
func (d *JsonDuration) UnmarshalText(b []byte) error {
	return d.parse(b)
}

// UnmarshalParam implements unmarshalling from a string.  This is
// required for the labstack/echo BindUnmarshaller API.
func (d *JsonDuration) UnmarshalParam(s string) error {
	return d.parse([]byte(s))
}

// get2digits is like strconv.Atoi, but it is much stricter, requiring
// exactly 2 decimal digits.  This is useful for very strict parsing
// of each component clock style times.
func (*JsonDuration) get2digits(s []byte) (uint64, error) {
	if len(s) != 2 || s[0] < '0' || s[0] > '9' || s[1] < '0' || s[1] > '9' {
		return 0, errInvalidJsonDuration
	}
	return uint64(s[0]-'0')*10 + uint64(s[1]-'0'), nil
}

// parse parses the given time from a string, which may be either a solitary
// integer count of days, a <d>.<hh>:<mm>:<ss> or <d>.<hh>:<mm>:<ss>.<sss>
// form.  The seconds may have precision down to nanosecond, but may also
// be supplied in any coarser precision.  For example, 10.5 seconds means
// 10500 milliseconds.
func (d *JsonDuration) parse(s []byte) error {

	if len(s) < 1 {
		return errInvalidJsonDuration
	}

	var neg bool

	if s[0] == '-' {
		neg = true
		s = s[1:]
		if len(s) < 1 {
			return errInvalidJsonDuration
		}
	}
	if !bytes.ContainsRune(s, ':') {

		dur, err := strconv.ParseUint(string(s), 10, 63)
		if err != nil {
			return errInvalidJsonDuration
		}

		// If 1e9 or greater then we assume the unit is nanoseconds and not days
		if dur >= 1e9 {
			d.Duration = time.Duration(dur)
		} else {
			d.Duration = 24 * time.Hour * time.Duration(dur)
		}
		if neg {
			d.Duration = -d.Duration
		}
		return nil
	}

	fields := bytes.Split(s, []byte{':'})
	if len(fields) != 3 {
		return errInvalidJsonDuration
	}

	var days, hours, mins, secs, nsecs uint64
	var err error

	if hd := bytes.SplitN(fields[0], []byte{'.'}, 2); len(hd) == 2 {
		if days, err = strconv.ParseUint(string(hd[0]), 10, 64); err != nil {
			return errInvalidJsonDuration
		}
		if hours, err = strconv.ParseUint(string(hd[1]), 10, 64); err != nil {
			return errInvalidJsonDuration
		}
	} else {
		if hours, err = strconv.ParseUint(string(fields[0]), 10, 64); err != nil {
			return errInvalidJsonDuration
		}
	}

	if mins, err = d.get2digits(fields[1]); err != nil {
		return errInvalidJsonDuration
	}

	// This part allows precision down to nsec, but smaller levels
	// of precision can be supplied.  For example, 10.5 turns into
	// 10500 milliseconds.
	if parts := bytes.SplitN(fields[2], []byte{'.'}, 2); len(parts) == 2 {
		if secs, err = d.get2digits(parts[0]); err != nil {
			return errInvalidJsonDuration
		}
		mult := 1.0
		val := 0.0
		for _, c := range parts[1] {
			mult /= 10
			if c < '0' || c > '9' || mult < 0.000000001 {
				return errInvalidJsonDuration
			}
			val += mult * float64(c-'0')
		}
		nsecs = uint64(val * float64(time.Second))
	} else {
		nsecs = 0
		if secs, err = d.get2digits(fields[2]); err != nil {
			return errInvalidJsonDuration
		}
	}

	d.Duration = time.Duration((days*24)+hours)*time.Hour +
		time.Duration(mins)*time.Minute +
		time.Duration(secs)*time.Second +
		time.Duration(nsecs)
	if neg {
		d.Duration = -d.Duration
	}
	return nil
}
