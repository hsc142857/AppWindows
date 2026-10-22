package core

import (
	"encoding/csv"
	"io"
	"strconv"
	"time"
)

// WriteCSV는 로그를 CSV로 쓴다 (Excel에서 한글이 깨지지 않도록 UTF-8 BOM을 붙인다).
// 앞 열은 수집 정보(대상·처음/마지막 확인·서버 전송), 뒤 열은 화면 표의 셀이다.
// 셀 열 이름은 대상이 하나면 그 화면의 머리글, 여러 대상이 섞이면 열1, 열2, …로 쓴다.
func WriteCSV(w io.Writer, entries []Entry, loc *time.Location) error {
	if loc == nil {
		loc = time.Local
	}
	if _, err := io.WriteString(w, "\xef\xbb\xbf"); err != nil {
		return err
	}
	width, single := 0, true
	var headers []string
	for i, e := range entries {
		width = max(width, len(e.Cells), len(e.Headers))
		if i > 0 && e.TargetURL != entries[0].TargetURL {
			single = false
		}
		if len(e.Headers) > len(headers) {
			headers = e.Headers
		}
	}
	cw := csv.NewWriter(w)
	head := []string{"번호", "대상", "URL", "처음 확인", "마지막 확인", "서버 전송", "순번", "uid"}
	for i := 0; i < width; i++ {
		name := "열" + strconv.Itoa(i+1)
		if single && i < len(headers) && headers[i] != "" {
			name = headers[i]
		}
		head = append(head, name)
	}
	if err := cw.Write(head); err != nil {
		return err
	}
	for _, e := range entries {
		synced := "대기"
		switch {
		case e.SyncedAt != "":
			synced = localString(e.SyncedAt, loc)
		case e.SyncError != "":
			synced = "거부: " + e.SyncError
		}
		row := []string{strconv.FormatInt(e.ID, 10), e.TargetName, e.TargetURL,
			localString(e.FirstSeenAt, loc), localString(e.LastSeenAt, loc), synced, strconv.Itoa(e.Occurrence), e.UID}
		row = append(row, e.Cells...)
		for len(row) < len(head) {
			row = append(row, "")
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func localString(s string, loc *time.Location) string {
	t, err := ParseTime(s)
	if err != nil {
		return s
	}
	return t.In(loc).Format("2006-01-02 15:04:05")
}
