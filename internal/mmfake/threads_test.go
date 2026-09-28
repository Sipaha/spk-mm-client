package mmfake

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

func TestPostThreadPagesUpWithoutGapsOrDuplicates(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	root := s.SeedThread("c-town", "alice", 150)
	a := loginAs(t, s, "alice")

	seen := map[string]bool{}
	var order []string
	fromCreateAt, fromPost := int64(0), ""
	for {
		path := "/api/v4/posts/" + root + "/thread?perPage=60&direction=up&collapsedThreads=false"
		if fromCreateAt > 0 {
			path += "&fromCreateAt=" + strconv.FormatInt(fromCreateAt, 10) + "&fromPost=" + fromPost
		}
		var page model.PostList
		require.Equal(t, 200, a.call("GET", path, nil, &page))
		require.Equal(t, root, page.Order[0], "order[0] is always the requested root")
		replies := page.Order[1:]
		require.LessOrEqual(t, len(replies), 60)
		for _, id := range replies {
			require.False(t, seen[id], "no duplicate id across pages: %s", id)
			seen[id] = true
			order = append(order, id)
		}
		if page.HasNext == nil || !*page.HasNext {
			break
		}
		last := page.Posts[replies[len(replies)-1]]
		fromCreateAt, fromPost = last.CreateAt, last.ID
	}
	assert.Len(t, seen, 150, "every reply seen exactly once")
	// pages are newest-first (direction=up): CreateAt strictly decreasing across the whole walk.
	for i := 1; i < len(order); i++ {
		p, q := createAtOf(t, s, order[i-1]), createAtOf(t, s, order[i])
		assert.Greater(t, p, q, "replies walked newest-first without gaps")
	}
}

// createAtOf is a tiny helper reading a single post's CreateAt via the store
// directly (VisiblePosts is channel-wide and order isn't guaranteed there).
func createAtOf(t *testing.T, s *Server, postID string) int64 {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.chat.byID[postID]
	require.NotNil(t, p)
	return p.CreateAt
}

func TestPostThreadHasNextBoundary(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	root := s.SeedThread("c-town", "alice", 5)
	a := loginAs(t, s, "alice")

	var exact model.PostList
	require.Equal(t, 200, a.call("GET", "/api/v4/posts/"+root+"/thread?perPage=5&direction=up", nil, &exact))
	require.NotNil(t, exact.HasNext)
	assert.False(t, *exact.HasNext, "exactly perPage replies: no more")

	var short model.PostList
	require.Equal(t, 200, a.call("GET", "/api/v4/posts/"+root+"/thread?perPage=4&direction=up", nil, &short))
	require.NotNil(t, short.HasNext)
	assert.True(t, *short.HasNext)
	assert.Len(t, short.Order, 5, "root + 4 replies, the 5th (oldest) shaved off")

	var whole model.PostList
	require.Equal(t, 200, a.call("GET", "/api/v4/posts/"+root+"/thread", nil, &whole))
	assert.Len(t, whole.Order, 6, "no perPage: the whole thread, root + 5 replies")
	assert.Nil(t, whole.HasNext)
}

func TestPostThreadErrors(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	root := s.SeedThread("c-town", "alice", 2)
	reply := s.VisiblePosts("c-town")
	require.NotEmpty(t, reply)
	replyID := ""
	for _, p := range reply {
		if p.RootID == root {
			replyID = p.ID
			break
		}
	}
	require.NotEmpty(t, replyID)
	a := loginAs(t, s, "alice")

	assert.Equal(t, 404, a.call("GET", "/api/v4/posts/unknown/thread", nil, nil), "unknown root")
	assert.Equal(t, 400, a.call("GET", "/api/v4/posts/"+root+"/thread?perPage=201", nil, nil), "perPage over the cap")
	assert.Equal(t, 400, a.call("GET", "/api/v4/posts/"+root+"/thread?fromPost=x", nil, nil), "fromPost without fromCreateAt")
	assert.Equal(t, 400, a.call("GET", "/api/v4/posts/"+replyID+"/thread?collapsedThreads=true", nil, nil), "collapsedThreads on a reply id")

	b := loginAs(t, s, "bob") // not a member of c-secret
	root2 := s.PostAs("c-secret", "alice", "root2")
	assert.Equal(t, 403, b.call("GET", "/api/v4/posts/"+root2.ID+"/thread", nil, nil))
}

func TestReplyToAReplyIsRejected(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	root := s.SeedThread("c-town", "alice", 1)
	var replyID string
	for _, p := range s.VisiblePosts("c-town") {
		if p.RootID == root {
			replyID = p.ID
		}
	}
	require.NotEmpty(t, replyID)
	a := loginAs(t, s, "alice")
	status := a.call("POST", "/api/v4/posts", map[string]any{"channel_id": "c-town", "root_id": replyID, "message": "nested"}, nil)
	assert.Equal(t, 400, status)
}

func TestReplyRootMustBeInTheSameChannel(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	root := s.PostAs("c-town", "alice", "root here")
	a := loginAs(t, s, "alice")
	status := a.call("POST", "/api/v4/posts", map[string]any{"channel_id": "c-offtopic", "root_id": root.ID, "message": "x"}, nil)
	assert.Equal(t, 400, status)
}

func TestReplyCarriesTheThreadsReplyCount(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	root := s.PostAs("c-town", "alice", "root")
	a := loginAs(t, s, "bob")
	var p1 model.Post
	require.Equal(t, 201, a.call("POST", "/api/v4/posts", map[string]any{"channel_id": "c-town", "root_id": root.ID, "message": "r1"}, &p1))
	assert.Equal(t, int64(1), p1.ReplyCount, "the reply itself carries the thread's reply_count")
	var p2 model.Post
	require.Equal(t, 201, a.call("POST", "/api/v4/posts", map[string]any{"channel_id": "c-town", "root_id": root.ID, "message": "r2"}, &p2))
	assert.Equal(t, int64(2), p2.ReplyCount)
}

func TestReplyDeletionDecrementsReplyCount(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	root := s.PostAs("c-town", "alice", "root")
	r1 := s.ReplyAs("c-town", root.ID, "bob", "r1")
	s.ReplyAs("c-town", root.ID, "carol", "r2")
	a := loginAs(t, s, "bob")
	require.Equal(t, 200, a.call("DELETE", "/api/v4/posts/"+r1.ID, nil, nil))

	var thread model.PostList
	require.Equal(t, 200, a.call("GET", "/api/v4/posts/"+root.ID+"/thread", nil, &thread))
	assert.Equal(t, int64(1), thread.Posts[root.ID].ReplyCount)
	assert.Len(t, thread.Order, 2, "root + the one surviving reply")
}

func TestFollowersOnPostedAndThreadUpdatedOnlyToCRTSubscribers(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	s.SetCollapsedThreads("always_on")
	root := s.PostAs("c-town", "alice", "root")

	aliceTok := loginAs(t, s, "alice")
	bobTok := loginAs(t, s, "bob")
	carolTok := loginAs(t, s, "carol")
	aliceWS := dialWS(t, s, aliceTok.tok, "")
	bobWS := dialWS(t, s, bobTok.tok, "")
	carolWS := dialWS(t, s, carolTok.tok, "")
	require.Equal(t, "hello", read(t, aliceWS).Event)
	require.Equal(t, "hello", read(t, bobWS).Event)
	require.Equal(t, "hello", read(t, carolWS).Event)

	s.ReplyAs("c-town", root.ID, "bob", "r1")

	// "posted" goes to every channel member (alice, bob, carol); each
	// connection's own copy of "followers" carries only their own id, and
	// only if they are in fact a follower (deliverLocked). Bob is the
	// replier, so followers never lists him even though he is a
	// participant; carol is a channel member but never touched the thread.
	pa := read(t, aliceWS)
	assert.Equal(t, "posted", pa.Event)
	assert.Equal(t, `["u-alice"]`, pa.Data["followers"], "alice (root author) is a follower")
	pb := read(t, bobWS)
	assert.Equal(t, "posted", pb.Event)
	_, hasFollowers := pb.Data["followers"]
	assert.False(t, hasFollowers, "the replier is never their own follower")
	pc := read(t, carolWS)
	assert.Equal(t, "posted", pc.Event)
	_, carolHasFollowers := pc.Data["followers"]
	assert.False(t, carolHasFollowers, "carol is a channel member but not a thread follower")

	// thread_updated goes to every CRT-enabled subscriber (root author +
	// replier here — carol never touched the thread).
	tu := read(t, aliceWS)
	assert.Equal(t, "thread_updated", tu.Event)
	tb := read(t, bobWS)
	assert.Equal(t, "thread_updated", tb.Event)
	noFrame(t, carolWS)
}

func TestThreadUpdatedNotSentWhenCRTIsOff(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	s.SetCollapsedThreads("disabled")
	root := s.PostAs("c-town", "alice", "root")
	aliceTok := loginAs(t, s, "alice")
	aliceWS := dialWS(t, s, aliceTok.tok, "")
	require.Equal(t, "hello", read(t, aliceWS).Event)

	s.ReplyAs("c-town", root.ID, "bob", "r1")
	f := read(t, aliceWS)
	assert.Equal(t, "posted", f.Event)
	noFrame(t, aliceWS) // CRT disabled: no thread_updated
}

func TestMarkThreadReadPublishesThreadReadChangedAnd404sWithoutSubscription(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	root := s.PostAs("c-town", "alice", "root")
	s.ReplyAs("c-town", root.ID, "bob", "r1")
	a := loginAs(t, s, "alice")

	var resp model.ThreadResponse
	status := a.call("PUT", "/api/v4/users/me/teams/t-fake/threads/"+root.ID+"/read/999999999999", nil, &resp)
	require.Equal(t, 200, status)
	assert.Equal(t, root.ID, resp.PostID)
	reads := s.ThreadReads()
	require.Len(t, reads, 1)
	assert.Equal(t, root.ID, reads[0].RootID)
	assert.Equal(t, "t-fake", reads[0].TeamID)

	carol := loginAs(t, s, "carol") // never subscribed to this thread
	status = carol.call("PUT", "/api/v4/users/me/teams/t-fake/threads/"+root.ID+"/read/1", nil, nil)
	assert.Equal(t, 404, status)
}

func TestTeamsUnreadIncludesThreadFieldsWithoutDMThreads(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	s.SetCollapsedThreads("always_on")
	root := s.PostAs("c-town", "alice", "root")
	s.ReplyAs("c-town", root.ID, "bob", "hey @alice")
	// A DM message auto-mentions the other member (mentionsLocked), so this
	// reply also mentions alice — the point of this test is that it must
	// NOT show up in ThreadMentionCount (DM/GM threads have team_id "").
	dmRoot := s.PostAs("c-dm-bob", "bob", "dm root")
	s.ReplyAs("c-dm-bob", dmRoot.ID, "bob", "dm reply")

	a := loginAs(t, s, "alice")
	var unread []model.TeamUnread
	require.Equal(t, 200, a.call("GET", "/api/v4/users/me/teams/unread?include_collapsed_threads=true", nil, &unread))
	require.Len(t, unread, 1)
	assert.Equal(t, "t-fake", unread[0].TeamID)
	assert.Equal(t, int64(1), unread[0].ThreadMentionCount, "the @alice mention in c-town's thread, not the DM thread")
}

func TestThreadTotalsOnlyAndExcludeDirect(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	s.SetCollapsedThreads("always_on")
	root := s.PostAs("c-town", "alice", "root")
	s.ReplyAs("c-town", root.ID, "bob", "r1")
	dmRoot := s.PostAs("c-dm-bob", "alice", "dm root")
	s.ReplyAs("c-dm-bob", dmRoot.ID, "bob", "dm reply")

	a := loginAs(t, s, "alice")
	var withDM model.ThreadTotals
	require.Equal(t, 200, a.call("GET", "/api/v4/users/me/teams/t-fake/threads?totalsOnly=true", nil, &withDM))
	assert.Equal(t, int64(2), withDM.Total, "channel thread + DM thread")

	var noDM model.ThreadTotals
	require.Equal(t, 200, a.call("GET", "/api/v4/users/me/teams/t-fake/threads?totalsOnly=true&excludeDirect=true", nil, &noDM))
	assert.Equal(t, int64(1), noDM.Total)

	status := a.call("GET", "/api/v4/users/me/teams/t-fake/threads?totalsOnly=true&threadsOnly=true", nil, nil)
	assert.Equal(t, 400, status, "totalsOnly and threadsOnly are mutually exclusive")
}

func TestFollowingAndSeedThread(t *testing.T) {
	s := Start(Options{SeedPosts: -1})
	defer s.Close()
	root := s.SeedThread("c-town", "alice", 4)
	assert.True(t, s.Following(root, "alice"), "root author auto-follows")
	assert.True(t, s.Following(root, "bob"))
	assert.True(t, s.Following(root, "carol"))
	assert.False(t, s.Following("unknown-root", "alice"))
	assert.Len(t, s.VisiblePosts("c-town"), 5, "root + 4 replies")
}
