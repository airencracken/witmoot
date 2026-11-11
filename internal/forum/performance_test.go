package forum

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkRecentPostsScale(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			s := testStore(b)
			ctx := context.Background()
			uid, err := s.CreateUser(ctx, "benchowner", "hash")
			if err != nil {
				b.Fatal(err)
			}
			topic, err := s.CreateTopic(ctx, 1, uid, "Benchmark conversation", "First message", AudienceMembers)
			if err != nil {
				b.Fatal(err)
			}
			_, err = s.db.Exec(`WITH RECURSIVE seq(x) AS(SELECT 1 UNION ALL SELECT x+1 FROM seq WHERE x<?) INSERT INTO posts(topic_id,author_id,body,created_at) SELECT ?,?,'A message for the benchmark',x FROM seq`, n, topic, uid)
			if err != nil {
				b.Fatal(err)
			}
			reader, err := s.UserByID(ctx, uid)
			if err != nil {
				b.Fatal(err)
			}
			for _, offset := range []int{0, n - 50} {
				b.Run(fmt.Sprint(offset), func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						rows, _, err := s.RecentPosts(ctx, 30, offset, &reader)
						if err != nil || len(rows) != 30 {
							b.Fatal(len(rows), err)
						}
					}
				})
			}
		})
	}
}
