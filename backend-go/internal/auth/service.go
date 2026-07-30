package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 复用 Django 既有表（accounts_user / iam_*），Go 侧只读写数据不改 schema——
// 绞杀期两栈共库，迁移完成后 schema 所有权移交 Go（goose/atlas 接管）。
type Service struct{ DB *pgxpool.Pool }

var ErrInvalidCredentials = errors.New("invalid credentials")

type UserRow struct {
	ID          string
	Username    string
	Password    string
	Nickname    string
	Phone       string
	Email       string
	Avatar      string
	Preferences map[string]any
	IsStaff     bool
	IsSuperuser bool
	IsActive    bool
	OrgID       *string
	OrgName     *string
	DateJoined  time.Time
	LastLogin   *time.Time
}

func (s *Service) userBy(ctx context.Context, where string, arg any) (*UserRow, error) {
	u := &UserRow{}
	err := s.DB.QueryRow(ctx, `
		SELECT u.id::text, u.username, u.password, u.nickname, COALESCE(u.phone,''), COALESCE(u.email,''),
		       COALESCE(u.avatar,''), COALESCE(u.preferences,'{}'::jsonb), u.is_staff, u.is_superuser, u.is_active,
		       u.organization_id::text, o.name, u.date_joined, u.last_login
		FROM accounts_user u
		LEFT JOIN iam_organization o ON o.id = u.organization_id
		WHERE `+where, arg,
	).Scan(&u.ID, &u.Username, &u.Password, &u.Nickname, &u.Phone, &u.Email,
		&u.Avatar, &u.Preferences, &u.IsStaff, &u.IsSuperuser, &u.IsActive,
		&u.OrgID, &u.OrgName, &u.DateJoined, &u.LastLogin)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	return u, err
}

// Authenticate 用户名+口令登录：口令走 Django pbkdf2 校验，成功后回写 last_login
// （对齐 SIMPLE_JWT.UPDATE_LAST_LOGIN）。
func (s *Service) Authenticate(ctx context.Context, username, password string) (*UserRow, error) {
	u, err := s.userBy(ctx, "u.username = $1", username)
	if err != nil {
		return nil, err
	}
	if !u.IsActive {
		return nil, ErrInvalidCredentials
	}
	ok, err := VerifyDjangoPassword(password, u.Password)
	if err != nil || !ok {
		return nil, ErrInvalidCredentials
	}
	_, _ = s.DB.Exec(ctx, "UPDATE accounts_user SET last_login = now() WHERE id = $1::uuid", u.ID)
	return u, nil
}

func (s *Service) UserByID(ctx context.Context, id string) (*UserRow, error) {
	return s.userBy(ctx, "u.id = $1::uuid", id)
}

// RolesAndPerms 取角色码/角色名/有效权限（超管 → ["*"]），对齐 iam.services.effective_permissions。
func (s *Service) RolesAndPerms(ctx context.Context, u *UserRow) (roles, roleNames, perms []string, err error) {
	roles, roleNames, perms = []string{}, []string{}, []string{}
	rows, err := s.DB.Query(ctx, `
		SELECT DISTINCT r.code, r.name FROM iam_role_assignment ra
		JOIN iam_role r ON r.id = ra.role_id
		WHERE ra.user_id = $1::uuid ORDER BY r.code`, u.ID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var code, name string
		if err = rows.Scan(&code, &name); err != nil {
			return
		}
		roles = append(roles, code)
		roleNames = append(roleNames, name)
	}
	if u.IsSuperuser {
		perms = []string{"*"}
		return
	}
	prows, err := s.DB.Query(ctx, `
		SELECT DISTINCT p.code FROM iam_role_assignment ra
		JOIN iam_role r ON r.id = ra.role_id AND r.is_active
		JOIN iam_role_permissions rp ON rp.role_id = r.id
		JOIN iam_permission p ON p.id = rp.permission_id
		WHERE ra.user_id = $1::uuid ORDER BY p.code`, u.ID)
	if err != nil {
		return roles, roleNames, perms, err
	}
	defer prows.Close()
	for prows.Next() {
		var code string
		if err = prows.Scan(&code); err != nil {
			return roles, roleNames, perms, err
		}
		perms = append(perms, code)
	}
	return
}

// DataScope 数据范围档（all/org_sub/org/self），对齐 iam.services.effective_data_scope。
func (s *Service) DataScope(ctx context.Context, u *UserRow) (string, error) {
	if u.IsSuperuser {
		return "all", nil
	}
	rank := map[string]int{"self": 0, "org": 1, "org_sub": 2, "all": 3}
	best := "self"
	rows, err := s.DB.Query(ctx, `
		SELECT r.data_scope FROM iam_role_assignment ra
		JOIN iam_role r ON r.id = ra.role_id AND r.is_active
		WHERE ra.user_id = $1::uuid`, u.ID)
	if err != nil {
		return best, err
	}
	defer rows.Close()
	for rows.Next() {
		var sc string
		if err := rows.Scan(&sc); err != nil {
			return best, err
		}
		if rank[sc] > rank[best] {
			best = sc
		}
	}
	return best, nil
}

// ScopeOrgIDs 返回组织可见范围：nil 表示全量可见；空切片表示不可见任何归属组织的数据。
// 对齐 iam.scoping.scope_queryset 的 path 前缀子树语义。
func (s *Service) ScopeOrgIDs(ctx context.Context, u *UserRow) ([]string, error) {
	scope, err := s.DataScope(ctx, u)
	if err != nil {
		return nil, err
	}
	if scope == "all" {
		return nil, nil
	}
	if u.OrgID == nil {
		return []string{}, nil
	}
	if scope == "self" || scope == "org" {
		return []string{*u.OrgID}, nil
	}
	// org_sub：物化路径前缀匹配
	rows, err := s.DB.Query(ctx, `
		SELECT o.id::text FROM iam_organization o
		WHERE o.path LIKE (SELECT COALESCE(NULLIF(path,''), id::text) FROM iam_organization WHERE id=$1::uuid) || '%'
		   OR o.id = $1::uuid`, *u.OrgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}
