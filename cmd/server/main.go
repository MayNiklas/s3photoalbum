package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var minioClient *minio.Client
var mediaBucket string
var thumbnailBucket string
var templatesDir string

var DB *gorm.DB

type User struct {
	gorm.Model
	Username string `json:"username"`
	Password string `json:"password"`
	Age      uint   `json:"age"`
}

func verifyToken(c *gin.Context) {

	token, err := c.Cookie("token")
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{})
		return
	}

	id, username, err := validateToken(token)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{})
		return
	}

	c.Set("id", id)
	c.Set("username", username)
	c.Next()
}

func getSession(c *gin.Context) (uint, string, bool) {

	fmt.Println("getting session")
	id, ok := c.Get("id")
	if !ok {
		return 0, "", false
	}
	username, ok := c.Get("username")
	if !ok {
		return 0, "", false
	}
	return id.(uint), username.(string), true
}

func main() {

	endpoint := os.Getenv("S3_ENDPOINT")
	accessKeyID := os.Getenv("S3_ACCESSKEY")
	secretAccessKey := os.Getenv("S3_SECRETKEY")
	mediaBucket = os.Getenv("S3_BUCKET_MEDIA")
	thumbnailBucket = os.Getenv("S3_BUCKET_THUMBNAILS")
	useSSL := true

	templatesDir = os.Getenv("TEMPLATES_DIR")
	if len(templatesDir) == 0 {
		templatesDir = "./templates"
	}

	var db *gorm.DB
	var err error

	// Setup database
	// TODO use an actual file for persistance
	db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	if err := db.AutoMigrate(&User{}); err != nil {
		panic(err)
	}
	DB = db

	// TODO users for testing
	pinPass, err := hashAndSalt("pin")
	if err != nil {
		log.Fatalln(err)
	}
	poxPass, err := hashAndSalt("pox")
	if err != nil {
		log.Fatalln(err)
	}
	_, _ = insertUser("pin", pinPass, 30)
	_, _ = insertUser("pox", poxPass, 25)

	// Initialize minio client object.
	minioClient, err = minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		log.Fatalln(err)
	}

	// Setup routes
	r := gin.Default()

	// r.Delims("{[{", "}]}")
	r.SetFuncMap(template.FuncMap{
		"incolumn": func(colNum, index int) bool { return index%4 == colNum },
	})

	r.LoadHTMLGlob(path.Join(templatesDir + "/*.html"))

	r.POST("/login", login)

	r.GET("/login", func(c *gin.Context) {
		c.HTML(http.StatusOK, "login.html", nil)
	})

	r.Static("/static", "./static")

	r.Use(verifyToken) //TODO fix
	r.GET("/me", getUserInfo)
	r.GET("/", indexHandler)
	r.GET("/albums/:album", albumHandler)
	r.GET("/albums/:album/:image", imageHandler)

	fmt.Println("starting gin")
	if err := r.Run("localhost:7788"); err != nil {
		panic(err)
	}

}

func albumHandler(c *gin.Context) {

	ad := struct {
		Title  string
		Images []string
	}{
		Title:  c.Param("album"),
		Images: listObjectsByPrefix(c.Param("album") + "/"),
	}

	c.HTML(http.StatusOK, "album.html", ad)
}

func imageHandler(c *gin.Context) {

	res := c.DefaultQuery("thumbnail", "false")
	thumbnail, err := strconv.ParseBool(res)
	if err != nil {
		thumbnail = false
	}

	imgPath := c.Param("album") + "/" + c.Param("image")

	// Set request parameters for content-disposition.
	reqParams := make(url.Values)
	// reqParams.Set("response-content-disposition", "attachment; filename=\""+ps.ByName("image")+"\"")

	var presignedURL *url.URL

	if thumbnail {

		thumbPath := imgPath + ".jpg"

		objInfo, err := minioClient.StatObject(context.Background(), thumbnailBucket, thumbPath, minio.StatObjectOptions{})
		if err != nil {

			errResponse := minio.ToErrorResponse(err)
			if errResponse.Code == "NoSuchKey" {
				// No thumbnails exists yet, fallback to full resolution
				fmt.Printf("No thumbnail found for '%v' falling back to full res\n", thumbPath)
				presignedURL, err = minioClient.PresignedGetObject(context.Background(), mediaBucket, imgPath, time.Second*1*60*60, reqParams)
				if err != nil {
					fmt.Println(err)
					return
				}

			} else {
				// A different error occured (e.g. access denied, bucket non-existant)
				log.Fatal(err)
			}

		} else {
			fmt.Println("Thumbnail exists:", objInfo)

			presignedURL, err = minioClient.PresignedGetObject(context.Background(), thumbnailBucket, thumbPath, time.Second*1*60*60, reqParams)
			if err != nil {
				fmt.Println(err)
				return
			}
			fmt.Println("getting thumb")
			fmt.Println(presignedURL)

		}

	} else {

		// Generates a presigned url which expires in a hour.
		presignedURL, err = minioClient.PresignedGetObject(context.Background(), mediaBucket, imgPath, time.Second*1*60*60, reqParams)
		if err != nil {
			fmt.Println(err)
			return
		}
	}
	// fmt.Println("Successfully generated presigned URL", presignedURL)
	c.Redirect(http.StatusSeeOther, presignedURL.String())
}

func listObjectsByPrefix(prefix string) []string {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// List objects
	objectCh := minioClient.ListObjects(ctx, mediaBucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: false,
	})

	ret := []string{}
	for object := range objectCh {
		if object.Err != nil {
			fmt.Println(object.Err)
			return ret
		}
		ret = append(ret, strings.TrimSuffix(object.Key, "/"))
	}
	return ret
}

func indexHandler(c *gin.Context) {

	tmpldata := struct {
		Title  string
		Albums []string
	}{
		Title:  "Albums",
		Albums: listObjectsByPrefix("/"),
	}

	c.HTML(http.StatusOK, "index.html", tmpldata)
}
