package master

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"sbpanel/internal/pb"
)

// Minute-precise ranges: series and exact dims use minute data; host dims use 5-minute data.
func TestMinuteRangeQueries(t *testing.T) {
	m := newTestMaster(t)
	seedNode(t, m, "n1")
	m.db.Exec(`INSERT INTO users(id, username, sub_token, uuid, password, created_at) VALUES(1,'u','t','x','y',0)`)
	base := time.Now().Add(-48*time.Hour).Unix() / 3600 * 3600 // an hour boundary two days ago
	// one report per minute for 30 minutes, 1000 bytes each, alternating hosts
	for i := 0; i < 30; i++ {
		host := "a.com"
		if i%2 == 1 {
			host = "b.com"
		}
		rep := &pb.TrafficReport{Seq: uint64(i + 1), WindowStart: base + int64(i)*60,
			Users: []*pb.UserStat{{User: "1", Inbound: "in", Outbound: "direct", Down: 1000}},
			Dims:  []*pb.DimStat{{User: "1", Inbound: "in", Outbound: "direct", Host: host, Port: 443, Down: 1000}}}
		if _, err := m.ingest("n1", rep); err != nil {
			t.Fatal(err)
		}
	}
	// [base+7min, base+19min): 12 minutes -> 12000 bytes, exact
	from, to := base+7*60, base+19*60
	q := "from=" + strconv.FormatInt(from, 10) + "&to=" + strconv.FormatInt(to, 10)
	rec := httptest.NewRecorder()
	m.apiSeries(rec, httptest.NewRequest("GET", "/api/traffic/series?step=60&group=total&"+q, nil))
	var s struct {
		Series []struct {
			Down   int64      `json:"down"`
			Points [][3]int64 `json:"points"`
		} `json:"series"`
	}
	json.Unmarshal(rec.Body.Bytes(), &s)
	if len(s.Series) != 1 || s.Series[0].Down != 12000 || len(s.Series[0].Points) != 12 {
		t.Fatalf("minute series: %s", rec.Body.String())
	}
	// coarser step over the same exact range stays exact (aggregated from minutes)
	rec = httptest.NewRecorder()
	m.apiSeries(rec, httptest.NewRequest("GET", "/api/traffic/series?step=3600&group=user&"+q, nil))
	json.Unmarshal(rec.Body.Bytes(), &s)
	if s.Series[0].Down != 12000 {
		t.Fatalf("hour-step over minute range: %s", rec.Body.String())
	}
	// exact dim: user total over the range, minute precision
	rec = httptest.NewRecorder()
	m.apiTop(rec, httptest.NewRequest("GET", "/api/traffic/top?dim=user&"+q, nil))
	var top []topRow
	json.Unmarshal(rec.Body.Bytes(), &top)
	if len(top) != 1 || top[0].Down != 12000 || rec.Header().Get("X-Precision") != "1m" {
		t.Fatalf("user top %s (%s)", rec.Body.String(), rec.Header().Get("X-Precision"))
	}
	// host dim: 5-minute buckets -> range widened to [5min, 20min) = 15 minutes
	rec = httptest.NewRecorder()
	m.apiTop(rec, httptest.NewRequest("GET", "/api/traffic/top?dim=host&"+q, nil))
	json.Unmarshal(rec.Body.Bytes(), &top)
	var sum int64
	for _, r := range top {
		sum += r.Down
	}
	if sum != 15000 || rec.Header().Get("X-Precision") != "5m" {
		t.Fatalf("host top sum %d (%s): %s", sum, rec.Header().Get("X-Precision"), rec.Body.String())
	}
	// validation
	for _, bad := range []string{"step=7&" + q, "step=60&from=100&to=50", "step=60&from=0&to=" + strconv.FormatInt(time.Now().Unix(), 10)} {
		rec = httptest.NewRecorder()
		m.apiSeries(rec, httptest.NewRequest("GET", "/api/traffic/series?"+bad, nil))
		if rec.Code != 400 {
			t.Errorf("%s -> %d", bad, rec.Code)
		}
	}
	// ranges older than the minute window report hour precision
	f := trafficFilter{from: time.Now().Add(-40 * 24 * time.Hour).Unix(), to: time.Now().Unix()}
	if m.topPrecision(f, "host") != "1h" || m.topPrecision(f, "user") != "1h" {
		t.Fatal("old range precision")
	}
}

// Detail filters (domain, source IP, ...) move series and exact rankings onto the 5-minute
// dimension tables, combine with AND across keys and OR within one, and series can split by domain.
func TestDetailFilters(t *testing.T) {
	m := newTestMaster(t)
	seedNode(t, m, "n1")
	seedNode(t, m, "n2")
	m.db.Exec(`INSERT INTO users(id, username, sub_token, uuid, password, created_at) VALUES(1,'u1','t1','x1','y1',0),(2,'u2','t2','x2','y2',0)`)
	base := time.Now().Add(-6*time.Hour).Unix() / 3600 * 3600
	type hit struct {
		node, user, out, host, src string
		down                       int64
	}
	hits := []hit{
		{"n1", "1", "direct", "www.a.com", "1.1.1.1", 1000},
		{"n1", "1", "proxy", "img.a.com", "1.1.1.1", 2000},
		{"n1", "2", "direct", "b.com", "2.2.2.2", 4000},
		{"n2", "2", "direct", "xa.com", "2.2.2.2", 8000}, // not a subdomain of a.com
		{"n2", "1", "direct", "a.com", "1.1.1.1", 16000},
	}
	seq := map[string]uint64{}
	for _, h := range hits {
		seq[h.node]++
		rep := &pb.TrafficReport{Seq: seq[h.node], WindowStart: base + int64(seq[h.node])*60,
			Users: []*pb.UserStat{{User: h.user, Inbound: "in", Outbound: h.out, Down: h.down}},
			Dims:  []*pb.DimStat{{User: h.user, Inbound: "in", Outbound: h.out, Host: h.host, Port: 443, SrcIp: h.src, Down: h.down}}}
		if _, err := m.ingest(h.node, rep); err != nil {
			t.Fatal(err)
		}
	}
	q := "from=" + strconv.FormatInt(base, 10) + "&to=" + strconv.FormatInt(base+3600, 10)
	top := func(dim, extra string) (map[string]int64, string) {
		rec := httptest.NewRecorder()
		m.apiTop(rec, httptest.NewRequest("GET", "/api/traffic/top?dim="+dim+"&"+q+extra, nil))
		var rows []topRow
		json.Unmarshal(rec.Body.Bytes(), &rows)
		out := map[string]int64{}
		for _, r := range rows {
			out[r.Key] = r.Down
		}
		return out, rec.Header().Get("X-Precision")
	}
	if got, p := top("node", "&domain=a.com"); got["n1"] != 3000 || got["n2"] != 16000 || len(got) != 2 || p != "5m" {
		t.Fatalf("node by domain a.com: %v %s", got, p)
	}
	if got, _ := top("domain", "&node=n1&outbound=direct"); got["a.com"] != 1000 || got["b.com"] != 4000 || len(got) != 2 {
		t.Fatalf("domain by node+outbound: %v", got)
	}
	if got, _ := top("user", "&src_ip=2.2.2.2&node=n1,n2"); got["2"] != 12000 || len(got) != 1 {
		t.Fatalf("user by src_ip: %v", got)
	}
	if got, p := top("user", "&node=n1"); got["1"] != 3000 || p != "1m" {
		t.Fatalf("exact dims stay on minute data: %v %s", got, p)
	}
	rec := httptest.NewRecorder()
	m.apiSeries(rec, httptest.NewRequest("GET", "/api/traffic/series?step=60&group=domain&"+q, nil))
	var s struct {
		Step      int64  `json:"step"`
		Precision string `json:"precision"`
		Series    []struct {
			Key  string `json:"key"`
			Down int64  `json:"down"`
		} `json:"series"`
	}
	json.Unmarshal(rec.Body.Bytes(), &s)
	got := map[string]int64{}
	for _, x := range s.Series {
		got[x.Key] = x.Down
	}
	if s.Step != 300 || s.Precision != "5m" || got["a.com"] != 19000 || got["b.com"] != 4000 || got["xa.com"] != 8000 {
		t.Fatalf("series by domain: %s", rec.Body.String())
	}
	// the LIKE pattern is escaped: "_" must not match any character
	if got, _ := top("node", "&domain=a_com"); len(got) != 0 {
		t.Fatalf("escaped domain: %v", got)
	}
}
