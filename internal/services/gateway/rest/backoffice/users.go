// Package backoffice is the admin REST surface under /backoffice/v1.
package backoffice

import (
	"github.com/gin-gonic/gin"

	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

type Users struct{ User userv1.UserServiceClient }

type userDTO struct {
	ID        string   `json:"id"`
	Username  string   `json:"username"`
	Name      string   `json:"name"`
	Phone     string   `json:"phone"`
	Email     string   `json:"email"`
	Status    string   `json:"status"`
	Roles     []string `json:"roles"`
	Points    int64    `json:"points"`
	Streak    int64    `json:"streak"`
	XP        int64    `json:"xp"`
	CreatedAt string   `json:"created_at"`
}

func toDTO(u *userv1.User) userDTO {
	roles := u.GetRoles()
	if roles == nil {
		roles = []string{} // JSON [] rather than null for a customer
	}
	return userDTO{
		ID: u.GetId(), Username: u.GetUsername(), Name: u.GetName(), Phone: u.GetPhone(),
		Email: u.GetEmail(), Status: u.GetStatus(), Roles: roles,
		Points: u.GetPoints(), Streak: u.GetStreak(), XP: u.GetXp(),
		CreatedAt: u.GetCreatedAt().AsTime().Format("2006-01-02T15:04:05.000Z"),
	}
}

func (h *Users) List(c *gin.Context) {
	res, err := h.User.ListUsers(c.Request.Context(), &userv1.ListUsersRequest{
		Query:  c.Query("query"),
		Status: c.Query("status"),
		Limit:  int32(httpx.IntQuery(c, "limit", 25)),
		Offset: int32(httpx.IntQuery(c, "offset", 0)),
		SortBy: c.Query("sort_by"), SortDir: c.Query("sort_dir"),
	})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	items := make([]userDTO, 0, len(res.GetUsers()))
	for _, u := range res.GetUsers() {
		items = append(items, toDTO(u))
	}
	httpx.OK(c, gin.H{"items": items, "total": res.GetTotal()})
}

type statusRequest struct {
	Status string `json:"status" binding:"required"`
}

func (h *Users) SetStatus(c *gin.Context) {
	body, ok := httpx.Bind[statusRequest](c)
	if !ok {
		return
	}
	res, err := h.User.SetUserStatus(c.Request.Context(), &userv1.SetUserStatusRequest{
		UserId: c.Param("id"), Status: body.Status,
	})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, toDTO(res.GetUser()))
}
