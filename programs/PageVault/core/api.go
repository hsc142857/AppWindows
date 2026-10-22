package core

import (
	"context"
	"os"
	"strings"

	"appwindows/shared/apiclient"
)

// SnapshotsBatchPath는 스냅샷 업로드 엔드포인트다 (저장소 docs/web-api.md).
const SnapshotsBatchPath = "/api/v1/pagevault/snapshots:batch"

// 한 요청의 대략적인 본문 상한. HTML이 커서 100건을 한 번에 보내면 수십 MB가 될 수 있다.
const maxBatchBytes = 8 << 20

// NewAPIConfig는 입력을 검증해 접속 설정을 만든다.
func NewAPIConfig(baseURL, token string) (apiclient.Config, error) {
	c := apiclient.Config{BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), Token: strings.TrimSpace(token)}
	return c, c.Validate()
}

// LoadAPIConfig는 api.json을 읽는다. 파일이 없으면 ok=false.
func LoadAPIConfig() (apiclient.Config, bool, error) { return apiclient.LoadConfig(APIConfigPath()) }

// SaveAPIConfig는 설정을 저장한다. 토큰은 Windows에서 DPAPI로 암호화된다 (shared/secret).
func SaveAPIConfig(c apiclient.Config) error { return apiclient.SaveConfig(APIConfigPath(), c) }

func ClearAPIConfig() error { return apiclient.ClearConfig(APIConfigPath()) }

// APISummary는 화면 표시용 한 줄 요약이다 (토큰은 가린다).
func APISummary() (summary string, configured bool) {
	c, ok, err := LoadAPIConfig()
	switch {
	case err != nil:
		return "설정 오류: " + err.Error(), false
	case !ok:
		if _, err := os.Stat(legacyPostgresPath()); err == nil {
			return "설정 안 됨 (이전 Postgres 설정 postgres.json은 더 이상 쓰지 않습니다)", false
		}
		return "설정 안 됨", false
	}
	return c.BaseURL + " (토큰 " + c.MaskedToken() + ")", true
}

func newAPIClient(c apiclient.Config) (*apiclient.Client, error) {
	return apiclient.New(c, "PageVault/"+Version)
}

// TestAPI는 health 엔드포인트로 접속과 토큰을 확인한다.
func TestAPI(ctx context.Context, c apiclient.Config) (string, error) {
	cl, err := newAPIClient(c)
	if err != nil {
		return "", err
	}
	h, err := cl.Health(ctx)
	if err != nil {
		return "", err
	}
	if !h.OK {
		return h.Version, errorf("서버가 정상 상태가 아니라고 응답했습니다")
	}
	return h.Version, nil
}

// SnapshotItem은 업로드하는 스냅샷 한 건이다 (서버 계약의 항목 형식).
type SnapshotItem struct {
	UID         string  `json:"uid"`
	TargetURL   string  `json:"targetURL"`
	TargetName  string  `json:"targetName"`
	FinalURL    string  `json:"finalURL"`
	Title       string  `json:"title"`
	Text        *string `json:"text"` // ok가 아니면 null
	HTML        *string `json:"html"`
	ContentHash string  `json:"contentHash"`
	Status      string  `json:"status"`
	Error       string  `json:"error"`
	CapturedAt  string  `json:"capturedAt"`
	LastSeenAt  string  `json:"lastSeenAt"`
}

func toItem(sn Snapshot) SnapshotItem {
	it := SnapshotItem{
		UID: sn.UID, TargetURL: sn.TargetURL, TargetName: sn.TargetName, FinalURL: sn.FinalURL,
		Title: sn.Title, ContentHash: sn.ContentHash, Status: sn.Status, Error: sn.Error,
		CapturedAt: sn.CapturedAt, LastSeenAt: sn.LastSeenAt,
	}
	if sn.Status == StatusOK {
		text, html := sn.Text, sn.HTML
		it.Text, it.HTML = &text, &html
	}
	return it
}

func itemSize(sn Snapshot) int {
	return len(sn.Text) + len(sn.HTML) + len(sn.Title) + len(sn.TargetURL) + len(sn.FinalURL) + 512
}

type SyncResult struct {
	APIConfigured bool `json:"apiConfigured"`
	Sent          int  `json:"sent"`      // 서버가 받은 건수
	Rejected      int  `json:"rejected"`  // 이번에 서버가 거부한 건수
	Remaining     int  `json:"remaining"` // 아직 보낼 건수 (거부된 것 제외)
}

// SyncConfigured는 저장된 API 설정으로 업로드한다. 설정이 없으면 아무것도 보내지 않는다.
func SyncConfigured(ctx context.Context, st *Store) (SyncResult, error) {
	c, ok, err := LoadAPIConfig()
	if err != nil || !ok {
		remaining, _ := st.UnsyncedCount()
		return SyncResult{Remaining: remaining}, err
	}
	return Sync(ctx, st, c)
}

// Sync는 미동기화 스냅샷을 최대 100건·약 8MB씩 업로드한다.
// 서버가 받은(accepted) 건 중 보낸 시점과 last_seen_at이 같은 행만 완료 표시하고,
// 거부(rejected)된 건은 sync_error에 사유를 남겨 다음 업로드에서 뺀다.
// 전송이 실패하면 남은 행은 synced_at NULL로 남아 다음에 다시 보낸다.
func Sync(ctx context.Context, st *Store, c apiclient.Config) (res SyncResult, err error) {
	res.APIConfigured = true
	defer func() { res.Remaining, _ = st.UnsyncedCount() }()

	cl, err := newAPIClient(c)
	if err != nil {
		return res, err
	}
	var afterID int64
	for {
		rows, err := st.UnsyncedAfter(afterID, apiclient.MaxBatchItems)
		if err != nil {
			return res, err
		}
		if len(rows) == 0 {
			return res, nil
		}
		// 크기 상한에 맞춰 앞에서부터 자른다 (한 건이 상한보다 커도 그 한 건은 보낸다).
		n, size := 0, 0
		for n < len(rows) && (n == 0 || size+itemSize(rows[n]) <= maxBatchBytes) {
			size += itemSize(rows[n])
			n++
		}
		batch := rows[:n]
		items := make([]SnapshotItem, len(batch))
		for i, sn := range batch {
			items[i] = toItem(sn)
		}
		out, err := apiclient.PostBatch(ctx, cl, SnapshotsBatchPath, items)
		if err != nil {
			return res, errorf("업로드 실패: %w", err)
		}
		accepted := make(map[string]bool, len(out.Accepted))
		for _, uid := range out.Accepted {
			accepted[uid] = true
		}
		done := make([]Snapshot, 0, len(batch))
		for _, sn := range batch {
			if accepted[sn.UID] {
				done = append(done, sn)
			}
		}
		if err := st.MarkSynced(done); err != nil {
			return res, err
		}
		if err := st.MarkRejected(out.Rejected); err != nil {
			return res, err
		}
		res.Sent += len(done)
		res.Rejected += len(out.Rejected)
		afterID = batch[len(batch)-1].ID
	}
}
