// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"context"
	"fmt"

	"witmoot/internal/forum"
)

const demoPassword = "demo-password"

type message struct{ author, body string }
type conversation struct {
	board    int64
	title    string
	mode     forum.Mode
	messages []message
}

var conversations = []conversation{
	{1, "Pull up a chair", forum.ModeOpen, []message{
		{"demo", "Welcome to our little round table.\n\nThis is a place for half-finished ideas, small victories, and keeping in touch. There is no catching up to do. Just come in when you feel like it."},
		{"freya", "I brought tea. Does putting the kettle on count as a small victory?"},
		{"jules", "Always. Especially if you remembered to put water in it."},
	}},
	{2, "Sunday soup and board games", forum.ModeOpen, []message{
		{"freya", "Soup at noon, games whenever everyone arrives. Bring a bowl and something you would like to play. Rain is an excellent excuse to stay another round."},
		{"demo", "I can bring bread. There may be slightly more bread than strictly necessary."},
	}},
	{3, "A birdhouse with a slightly wonky roof", forum.ModeOpen, []message{
		{"jules", "The scrap-wood birdhouse is finally up. The roof is crooked, but I am choosing to call it character.\n\nNext project: a little shelf for the kitchen."},
		{"freya", "Please keep the wonky roof. I would like to live in a house with character too."},
	}},
	{4, "Good books for a rainy afternoon", forum.ModeOpen, []message{
		{"freya", "What have you been reading lately? I am looking for something to go with a blanket and a mug of tea."},
		{"jules", "A field guide to local birds. The chapter on sparrows has become unexpectedly relevant."},
	}},
	{5, "How we use this little corner", forum.ModeOpen, []message{
		{"demo", "Start a conversation in any room, reply when you have something to add, and use Search to find it again.\n\nSign in as demo to try Settings and Invites, or as freya or jules to see the member view. Everything here is throwaway."},
	}},
	{2, "Dinner plans for the regulars", forum.ModePrivate, []message{
		{"demo", "This conversation is members-only. Sign out and it disappears from the board, recent conversations, and search."},
		{"jules", "I will bring the big serving bowl."},
	}},
	{5, "A little note for the host", forum.ModePersonal, []message{
		{"demo", "Only owners can read this conversation. Members and signed-out visitors cannot see it.\n\nTry changing the board mode under Settings; existing conversations keep their audience."},
	}},
}

func seed(ctx context.Context, store *forum.Store) error {
	users, err := seedUsers(ctx, store)
	if err != nil {
		return err
	}
	for _, topic := range conversations {
		if err := seedConversation(ctx, store, users, topic); err != nil {
			return fmt.Errorf("%s: %w", topic.title, err)
		}
	}
	if err := store.SetMode(ctx, forum.ModeOpen); err != nil {
		return err
	}
	return seedPrivateBoard(ctx, store, users)
}

func seedPrivateBoard(ctx context.Context, store *forum.Store, users map[string]int64) error {
	id, err := store.SaveBoard(ctx, users["demo"], forum.Board{Name: "The planning nook", Category: "A smaller table", Description: "A private board: freya can post, jules can read, and everyone else stays outside.", Restricted: true}, map[int64]string{users["freya"]: "write", users["jules"]: "read"})
	if err != nil {
		return err
	}
	topic, err := store.CreateTopic(ctx, id, users["demo"], "A little surprise for Sunday", "This board is only visible to its selected members. Owners choose No access, Read only, or Read and post under Manage boards.\n\nFreya can join in. Jules can read along. Sign out and the whole board disappears.", forum.AudienceMembers)
	if err != nil {
		return err
	}
	post, _, err := store.Reply(ctx, topic, users["freya"], "I will bring cake on Saturday.")
	if err != nil {
		return err
	}
	_, err = store.EditPost(ctx, post, users["freya"], "I will bring cake on Sunday.\n\nCorrected the day: use Edit on one of your messages to try it. The timestamp lets everyone know it changed.", 0)
	return err
}

func seedUsers(ctx context.Context, store *forum.Store) (map[string]int64, error) {
	hash, err := forum.HashPassword(demoPassword)
	if err != nil {
		return nil, err
	}
	if err := store.CreateOwner(ctx, "demo", hash); err != nil {
		return nil, err
	}
	owner, _, err := store.Credentials(ctx, "demo")
	if err != nil {
		return nil, err
	}
	users := map[string]int64{"demo": owner.ID}
	for _, name := range []string{"freya", "jules"} {
		id, err := store.CreateUser(ctx, name, hash)
		if err != nil {
			return nil, err
		}
		users[name] = id
	}
	return users, nil
}

func seedConversation(ctx context.Context, store *forum.Store, users map[string]int64, topic conversation) error {
	if err := store.SetMode(ctx, topic.mode); err != nil {
		return err
	}
	first := topic.messages[0]
	id, err := store.CreateTopic(ctx, topic.board, users[first.author], topic.title, first.body, topic.mode.Audience())
	if err != nil {
		return err
	}
	for _, reply := range topic.messages[1:] {
		if _, _, err := store.Reply(ctx, id, users[reply.author], reply.body); err != nil {
			return err
		}
	}
	return nil
}
