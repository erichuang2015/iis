package action

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coyove/iis/common"
	"github.com/coyove/iis/dal"
	"github.com/coyove/iis/ik"
	"github.com/coyove/iis/middleware"
	"github.com/coyove/iis/model"
	"github.com/gin-gonic/gin"
)

func APISignup(g *gin.Context) {
	var (
		ip       = hashIP(g)
		username = sanUsername(g.PostForm("username"))
		email    = common.SoftTrunc(g.PostForm("email"), 64)
		password = common.SoftTrunc(g.PostForm("password"), 32)
	)

	if len(username) < 3 || len(password) < 3 {
		g.String(200, "internal/error")
		return
	}

	if ret := checkCaptcha(g); ret != "" {
		g.String(200, ret)
		return
	}

	switch username := strings.ToLower(username); {
	case strings.HasPrefix(username, "master"), strings.HasPrefix(username, "admin"):
		g.String(200, "id/already-existed")
		return
	case strings.HasPrefix(username, strings.ToLower(common.Cfg.AdminName)):
		if admin, _ := dal.GetUser(common.Cfg.AdminName); admin != nil {
			g.String(200, "id/already-existed")
			return
		}
	}

	pwdHash := hmac.New(sha256.New, common.Cfg.KeyBytes)
	pwdHash.Write([]byte(password))
	u := &model.User{}
	u.ID = username
	u.Session = genSession()
	u.Email = email
	u.PasswordHash = pwdHash.Sum(nil)
	u.DataIP = "{" + ip + "}"
	u.TSignup = uint32(time.Now().Unix())
	u.TLogin = uint32(time.Now().Unix())
	tok := ik.MakeUserToken(u)

	if err := dal.Do(dal.NewRequest(dal.DoUpdateUser,
		"ID", u.ID,
		"Signup", true,
		"Session", u.Session,
		"Email", u.Email,
		"PasswordHash", u.PasswordHash,
		"DataIP", u.DataIP,
		"TSignup", u.TSignup,
		"TLogin", u.TLogin,
	)); err != nil {
		g.String(200, err.Error())
		return
	}

	g.SetCookie("id", tok, 365*86400, "", "", false, false)
	g.String(200, "ok")
}

func APILogin(g *gin.Context) {
	if ret := checkIP(g); ret != "" {
		g.String(200, ret)
		return
	}

	u, _ := dal.GetUser(sanUsername(g.PostForm("username")))
	if u == nil {
		g.String(200, "id/too-short")
		return
	}

	pwdHash := hmac.New(sha256.New, common.Cfg.KeyBytes)
	pwdHash.Write([]byte(common.SoftTrunc(g.PostForm("password"), 32)))
	if !bytes.Equal(u.PasswordHash, pwdHash.Sum(nil)) {
		g.String(200, "internal/error")
		return
	}

	u.Session = genSession()
	u.TLogin = uint32(time.Now().Unix())

	if ips := append(strings.Split(u.DataIP, ","), hashIP(g)); len(ips) > 5 {
		u.DataIP = strings.Join(append(ips[:1], ips[len(ips)-4:]...), ",")
	} else {
		u.DataIP = strings.Join(ips, ",")
	}

	tok := ik.MakeUserToken(u)

	if err := dal.Do(dal.NewRequest(dal.DoUpdateUser,
		"ID", u.ID,
		"Session", u.Session,
		"TLogin", u.TLogin,
		"DataIP", u.DataIP,
	)); err != nil {
		g.String(200, err.Error())
	} else {
		g.SetCookie("id", tok, 365*86400, "", "", false, false)
		g.String(200, "ok")
	}
}

func APIUserKimochi(g *gin.Context) {
	u := dal.GetUserByContext(g)
	if u == nil {
		g.String(200, "internal/error")
		return
	}

	k, _ := strconv.Atoi(g.PostForm("k"))
	if k < 0 || k > 44 {
		k = 25
	}

	if err := dal.Do(dal.NewRequest(dal.DoUpdateUser,
		"ID", u.ID,
		"Kimochi", byte(k),
	)); err != nil {
		g.String(200, "internal/error")
		return
	}
	g.String(200, "ok")
}

func APISearch(g *gin.Context) {
	uids := common.SearchUsers(g.PostForm("id"), 10)
	for i := range uids {
		uids[i] = "@" + uids[i]
	}
	tags := common.SearchTags(g.PostForm("id"), 10)
	for _, t := range tags {
		uids = append(uids, "#"+t)
	}
	g.JSON(200, uids)
}

func APINewCaptcha(g *gin.Context) {
	var p struct {
		UUID      string
		Challenge string
	}
	p.UUID, p.Challenge = ik.MakeToken(g)
	g.JSON(200, p)
}

func APILike(g *gin.Context) {
	var (
		redir = func(b string) { g.String(200, b) }
		u     = dal.GetUserByContext(g)
	)

	if u == nil {
		redir("internal/error")
		return
	}

	if ret := checkIP(g); ret != "" {
		redir(ret)
		return
	}

	to := g.PostForm("to")
	if to == "" {
		redir("internal/error")
		return
	}

	err := dal.LikeArticle(u.ID, to, g.PostForm("like") != "")
	if err != nil {
		redir(err.Error())
	} else {
		redir("ok")
	}
}

func APILogout(g *gin.Context) {
	u := dal.GetUserByContext(g)
	if u != nil {
		dal.Do(dal.NewRequest(dal.DoUpdateUser,
			"ID", u.ID,
			"Session", genSession(),
		))
		u = &model.User{}
		g.SetCookie("id", ik.MakeUserToken(u), 365*86400, "", "", false, false)
	}
	g.Status(200)
}

func APIFollowBlock(g *gin.Context) {
	u, to := dal.GetUserByContext(g), g.PostForm("to")
	if u == nil || to == "" || u.ID == to {
		g.String(200, "internal/error")
		return
	}

	if ret := checkIP(g); ret != "" {
		g.String(200, ret)
		return
	}

	var err error
	if g.PostForm("method") == "follow" {
		err = dal.FollowUser(u.ID, to, g.PostForm("follow") != "")
	} else {
		err = dal.BlockUser(u.ID, to, g.PostForm("block") != "")
	}

	if err != nil {
		g.String(200, err.Error())
	} else {
		g.String(200, "ok")
	}
	return
}

func APIFollowBlockSearch(g *gin.Context) {
	u := dal.GetUserByContext(g)
	if u == nil {
		g.String(200, "/user")
		return
	}

	q := g.PostForm("q")
	if strings.HasPrefix(q, "#") {
		g.String(200, "/tag/"+q[1:])
		return
	}

	id := dal.MakeID(g.PostForm("method"), u.ID, q)
	if a, _ := dal.GetArticle(id); a != nil {
		g.String(200, "/user")
		return
	}

	if _, err := dal.GetUser(q); err != nil {
		if res := common.SearchUsers(q, 1); len(res) > 0 {
			q = res[0]
		} else {
			q = ""
		}
	}
	g.String(200, "/t/"+q)
}

func APIUpdateUserSettings(g *gin.Context) {
	u := dal.GetUserByContext(g)
	if u == nil {
		g.String(200, "internal/error")
		return
	}

	switch {
	case g.PostForm("set-email") != "":
		if err := dal.Do(dal.NewRequest(dal.DoUpdateUser, "ID", u.ID,
			"Email", common.SoftTrunc(g.PostForm("email"), 256))); err != nil {
			g.String(200, err.Error())
			return
		}
	case g.PostForm("set-autonsfw") != "":
		if err := dal.Do(dal.NewRequest(dal.DoUpdateUser,
			"ID", u.ID,
			"SettingAutoNSFW", g.PostForm("autonsfw") != "",
		)); err != nil {
			g.String(200, err.Error())
			return
		}
	case g.PostForm("set-foldimg") != "":
		if err := dal.Do(dal.NewRequest(dal.DoUpdateUser,
			"ID", u.ID,
			"SettingFoldImages", g.PostForm("foldimg") != "",
		)); err != nil {
			g.String(200, err.Error())
			return
		}
	case g.PostForm("set-description") != "":
		if err := dal.Do(dal.NewRequest(dal.DoUpdateUser,
			"ID", u.ID,
			"SettingDescription", common.SoftTrunc(g.PostForm("description"), 512),
		)); err != nil {
			g.String(200, err.Error())
			return
		}
	case g.PostForm("set-custom-name") != "":
		name := g.PostForm("custom-name")
		if strings.Contains(strings.ToLower(name), "admin") && !u.IsAdmin() {
			name = strings.Replace(name, "admin", "nimda", -1)
		}
		name = common.SoftTruncDisplayWidth(name, 16)
		r := dal.NewRequest(dal.DoUpdateUser, "ID", u.ID, "CustomName", name)
		if err := dal.Do(r); err != nil {
			g.String(200, err.Error())
			return
		}
		g.Writer.Header().Add("X-Result",
			url.PathEscape(middleware.RenderTemplateString("display_name.html",
				r.UpdateUserRequest.Response.User)))
		g.Writer.Header().Add("X-Custom-Name", url.PathEscape(name))
	case g.PostForm("set-avatar") != "":
		_, err := writeAvatar(u, g.PostForm("avatar"))
		if err != nil {
			g.String(200, err.Error())
			return
		}
		if err := dal.Do(dal.NewRequest(dal.DoUpdateUser, "ID", u.ID, "Avatar", uint32(time.Now().Unix()))); err != nil {
			g.String(200, err.Error())
			return
		}
	}
	g.String(200, "ok")
}

func APIUpdateUserPassword(g *gin.Context) {
	u := dal.GetUserByContext(g)
	if u == nil {
		g.String(200, "internal/error")
		return
	}
	if res := checkIP(g); res != "" {
		g.String(200, res)
		return
	}

	oldPassword := common.SoftTrunc(g.PostForm("old-password"), 32)
	newPassword := common.SoftTrunc(g.PostForm("new-password"), 32)

	pwdHash := hmac.New(sha256.New, common.Cfg.KeyBytes)
	pwdHash.Write([]byte(oldPassword))
	if len(newPassword) < 3 || !bytes.Equal(u.PasswordHash, pwdHash.Sum(nil)) {
		g.String(200, "password/invalid-too-short")
		return
	}

	pwdHash.Reset()
	pwdHash.Write([]byte(newPassword))

	if err := dal.Do(dal.NewRequest(dal.DoUpdateUser,
		"ID", u.ID,
		"PasswordHash", pwdHash.Sum(nil),
	)); err != nil {
		g.String(200, err.Error())
		return
	}
	g.String(200, "ok")
}
