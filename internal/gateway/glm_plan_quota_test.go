package gateway

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// A GLM Coding Plan key that has spent its 5-hour, weekly or monthly window
// is out of quota, not rate limited, whichever endpoint refused it. Through
// the Anthropic endpoint Zhipu types the 429 rate_limit_error, and only the
// words say the window is spent and when it is back — which rateWords took
// for a rate limit before usedUpWords (上限) was reached, so the key rested
// as one: believing the reset it named, until then, and a window its owner
// reset by hand never lifted it (#1024 built the plan's key to rest to its
// window and come back when it renews).
func TestGLMPlanWindowSpentIsQuota(t *testing.T) {
	// the refusal as the Anthropic endpoint gives it, its window's reset
	// three hours off as Zhipu writes it (no zone, read as Beijing's)
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	said := "您已达到每周/每月使用上限，您的限额将在 " + reset.In(zhipuZone).Format("2006-01-02 15:04:05") + " 重置。"
	body := []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"[1310][` + said + `][20261020091843ac61fce173a347a5]"}}`)
	if got := failure(429, body); got != failQuota {
		t.Fatalf("the Anthropic endpoint's refusal read as %s, want quota", got)
	}
	// the same words escaped, as a JSON error may carry them (#1669)
	esc := []byte(`{"type":"error","error":{"type":"rate_limit_error","message":` + strconv.QuoteToASCII(said) + `}}`)
	if got := failure(429, esc); got != failQuota {
		t.Fatalf("escaped, read as %s, want quota", got)
	}
	// Zhipu's per-minute refusal says nothing of a window: it keeps its
	// backoff (#1024)
	if got := failure(429, []byte(`{"error":{"code":"1302","message":"请求太频繁，请稍后再试"}}`)); got != failRate {
		t.Fatalf("the per-minute refusal read as %s, want rate", got)
	}

	// out of quota the plan's key rests to the window it named, and is read
	// again as it is planned, so a window renewed — or reset by hand —
	// lifts it
	forgetRouting()
	oldAllow := allowances
	defer func() { allowances = oldAllow }()
	allowances = func(string) map[string]provider.Allowance { return nil }
	oldKey := keyAllowance
	defer func() { keyAllowance = oldKey }()
	var reread bool
	keyAllowance = func(provider.Provider) (provider.Allowance, bool) { reread = true; return nil, false }

	p := provider.Provider{ID: "zhipu", Name: "Zhipu GLM", Chat: "https://open.bigmodel.cn/api/coding/paas/v4", Key: "sk-plan-a"}
	id := provider.KeyAllowanceID(p)
	if id == "" {
		t.Fatal("a plan key has no id for its windows renewed")
	}
	c := candidate{p: p, model: "glm-5.3", rest: p.ID}
	s := &Server{}

	r := s.restAfter(c, 429, http.Header{}, body)
	if r.Why != failQuota || r.By != "resets" || r.user != id {
		t.Fatalf("rest %v by %s (%s) on %q, want quota by resets on %q", time.Until(r.Until), r.By, r.Why, r.user, id)
	}
	if d := time.Until(r.Until); d < 2*time.Hour+59*time.Minute || d > 3*time.Hour+time.Minute {
		t.Fatalf("rests %v, want about 3h (the time Zhipu named)", d)
	}
	reread = false
	if _, ok := restingOf(c); !ok || !reread {
		t.Fatalf("a key out of its window isn't read again as it is planned (reread=%v)", reread)
	}
	renewed("", id)
	if _, ok := restOf(c.restKey()); ok {
		t.Fatal("still resting after its window was found renewed")
	}
}
