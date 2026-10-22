package core

import (
	"context"
	"strings"

	"appwindows/shared/apiclient"
)

// EntriesBatchPath는 활동 로그 업로드 엔드포인트다 (저장소 docs/web-api.md).
const EntriesBatchPath = "/api/v1/activitylog/entries:batch"

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
		return "설정 안 됨 (로컬에만 저장)", false
	}
	return c.BaseURL + " (토큰 " + c.MaskedToken() + ")", true
}

func newAPIClient(c apiclient.Config) (*apiclient.Client, error) {
	return apiclient.New(c, "ActivityLogKeeper/"+Version)
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

// EntryItem은 업로드하는 활동 로그 한 행이다 (서버 계약의 항목 형식).
type EntryItem struct {
	UID         string   `json:"uid"`
	TargetURL   string   `json:"targetURL"`
	TargetName  string   `json:"targetName"`
	Headers     []string `json:"headers"`
	Cells       []string `json:"cells"`
	RowHash     string   `json:"rowHash"`
	Occurrence  int      `json:"occurrence"`
	FirstSeenAt string   `json:"firstSeenAt"`
}

func toItem(e Entry) EntryItem {
	return EntryItem{
		UID: e.UID, TargetURL: e.TargetURL, TargetName: e.TargetName, Headers: e.Headers, Cells: e.Cells,
		RowHash: e.RowHash, Occurrence: e.Occurrence, FirstSeenAt: e.FirstSeenAt,
	}
}

type SyncResult struct {
	APIConfigured bool `json:"apiConfigured"`
	Sent          int  `json:"sent"`      // 서버가 받은 건수
	Rejected      int  `json:"rejected"`  // 이번에 서버가 거부한 건수
	Remaining     int  `json:"remaining"` // 아직 보낼 건수 (거부된 것 제외)
}

// SyncConfigured는 저장된 API 설정으로 업로드한다. 설정이 없으면 아무것도 보내지 않는다 (로컬 보관만).
func SyncConfigured(ctx context.Context, st *Store) (SyncResult, error) {
	c, ok, err := LoadAPIConfig()
	if err != nil || !ok {
		remaining, _ := st.UnsyncedCount()
		return SyncResult{Remaining: remaining}, err
	}
	return Sync(ctx, st, c)
}

// Sync는 미전송 로그를 100건씩 업로드한다.
// 서버가 받은(accepted) 건만 완료로 표시하고, 거부(rejected)된 건은 sync_error에 사유를 남겨 다음 업로드에서 뺀다.
// 전송이 실패하면 남은 행은 synced_at NULL로 남아 다음 라운드에 다시 보낸다 (uid가 같아 서버에서 중복되지 않음).
func Sync(ctx context.Context, st *Store, c apiclient.Config) (res SyncResult, err error) {
	res.APIConfigured = true
	defer func() { res.Remaining, _ = st.UnsyncedCount() }()

	cl, err := newAPIClient(c)
	if err != nil {
		return res, err
	}
	var afterID int64
	for {
		batch, err := st.UnsyncedAfter(afterID, apiclient.MaxBatchItems)
		if err != nil {
			return res, err
		}
		if len(batch) == 0 {
			return res, nil
		}
		items := make([]EntryItem, len(batch))
		for i, e := range batch {
			items[i] = toItem(e)
		}
		out, err := apiclient.PostBatch(ctx, cl, EntriesBatchPath, items)
		if err != nil {
			return res, errorf("업로드 실패: %w", err)
		}
		accepted := make(map[string]bool, len(out.Accepted))
		for _, uid := range out.Accepted {
			accepted[uid] = true
		}
		done := make([]Entry, 0, len(batch))
		for _, e := range batch {
			if accepted[e.UID] {
				done = append(done, e)
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
