package forum

import (
	"context"
	"database/sql"
	"errors"
)

// CommunityPolicy preserves the audience boundaries needed by a future
// importer, without operational state or credential material.
type CommunityPolicy struct {
	Mode         string                 `json:"mode"`
	HouseRules   string                 `json:"house_rules,omitempty"`
	OwnerContact string                 `json:"owner_contact,omitempty"`
	Boards       []communityBoard       `json:"boards"`
	Groups       []communityGroup       `json:"groups"`
	MemberGrants []communityMemberGrant `json:"member_grants"`
	GroupGrants  []communityGroupGrant  `json:"group_grants"`
	GroupMembers []communityGroupMember `json:"group_members"`
}
type communityBoard struct {
	ID          int64  `json:"id"`
	Description string `json:"description"`
	Position    int    `json:"position"`
	Restricted  bool   `json:"restricted"`
	Archived    bool   `json:"archived"`
}
type communityGroup struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}
type communityMemberGrant struct {
	BoardID int64  `json:"board_id"`
	UserID  int64  `json:"user_id"`
	Access  string `json:"access"`
}
type communityGroupGrant struct {
	BoardID int64  `json:"board_id"`
	GroupID int64  `json:"group_id"`
	Access  string `json:"access"`
}
type communityGroupMember struct {
	GroupID int64 `json:"group_id"`
	UserID  int64 `json:"user_id"`
}

func communityPolicy(ctx context.Context, tx *sql.Tx) (*CommunityPolicy, error) {
	policy := &CommunityPolicy{}
	if err := tx.QueryRowContext(ctx, "SELECT mode FROM settings WHERE id=1").Scan(&policy.Mode); err != nil {
		return nil, err
	}
	err := tx.QueryRowContext(ctx, "SELECT house_rules,owner_contact FROM instance_branding WHERE id=1").Scan(&policy.HouseRules, &policy.OwnerContact)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	policy.Boards, err = collect(ctx, tx, "SELECT id,description,position,restricted,archived FROM boards ORDER BY position", nil, func(rows *sql.Rows) (b communityBoard, err error) {
		return b, rows.Scan(&b.ID, &b.Description, &b.Position, &b.Restricted, &b.Archived)
	})
	if err != nil {
		return nil, err
	}
	policy.Groups, err = collect(ctx, tx, "SELECT id,name,description FROM user_groups ORDER BY id", nil, func(rows *sql.Rows) (g communityGroup, err error) {
		return g, rows.Scan(&g.ID, &g.Name, &g.Description)
	})
	if err != nil {
		return nil, err
	}
	policy.MemberGrants, err = collect(ctx, tx, "SELECT board_id,user_id,access FROM board_members ORDER BY board_id,user_id", nil, func(rows *sql.Rows) (g communityMemberGrant, err error) {
		return g, rows.Scan(&g.BoardID, &g.UserID, &g.Access)
	})
	if err != nil {
		return nil, err
	}
	policy.GroupGrants, err = collect(ctx, tx, "SELECT board_id,group_id,access FROM board_groups ORDER BY board_id,group_id", nil, func(rows *sql.Rows) (g communityGroupGrant, err error) {
		return g, rows.Scan(&g.BoardID, &g.GroupID, &g.Access)
	})
	if err != nil {
		return nil, err
	}
	policy.GroupMembers, err = collect(ctx, tx, "SELECT group_id,user_id FROM group_members ORDER BY group_id,user_id", nil, func(rows *sql.Rows) (m communityGroupMember, err error) { return m, rows.Scan(&m.GroupID, &m.UserID) })
	return policy, err
}
