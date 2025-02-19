package main

import (
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"net/http"
)

func login(c *gin.Context) {

	formUser := c.PostForm("username")
	formPass := c.PostForm("password")

	user, err := findUserByUsername(formUser)
	if err != nil {
		// User not found
		c.HTML(http.StatusOK, "login.html", "Authentication failed")
		return
	}

	// Comparing the password with the hash
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(formPass)); err != nil {
		c.HTML(http.StatusOK, "login.html", "Authentication failed")
		return
	}

	token, err := generateToken(*user)
	if err != nil {
		c.HTML(http.StatusOK, "login.html", "Authentication failed")
		return
	}

	// func (c *Context) SetCookie(name, value string, maxAge int, path, domain string, secure, httpOnly bool)
	// TODO check parameters
	// TODO refersh the token before it expires
	c.SetCookie("token", token, 3600, "/", "localhost", true, false)

	c.Redirect(http.StatusSeeOther, "/")

}

func getUserInfo(c *gin.Context) {
	id, _, ok := getSession(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{})
		return
	}
	user, err := findUserByID(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{})
		return
	}
	c.JSON(http.StatusOK, user)
}
