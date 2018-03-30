/*Command rssfs is a 9p filesystem serving contents of RSS feeds.

	go get github.com/rbns/rssfs

Usage

	./rssfs [OPTIONS] URL [URL URL ...]
	-addr string
			listen address (default "localhost:9999")
	-debug
			enable debug mode
	-gid string
			gid name (default "nogroup")
	-uid string
			uid name (default "nobody")

Example

	$ ./rssfs https://www.kernel.org
	$ mount -t9p -o port=9999,noextend 127.0.0.1 /mnt/tmp
	$ tree /mnt/tmp/ | head -14
	/mnt/tmp/
	└── The Linux Kernel Archives
		├── About Linux Kernel
		│   ├── content
		│   ├── description
		│   ├── guid
		│   ├── link
		│   └── title
		├── Active kernel releases
		│   ├── content
		│   ├── description
		│   ├── guid
		│   ├── link
		│   └── title
	$ cat /mnt/tmp/The\ Linux\ Kernel\ Archives/About\ Linux\ Kernel/description | head
	<div class="section" id="what-is-linux">
	<h2>What is Linux?</h2>
	<p>Linux is a clone of the operating system Unix, written from scratch by
	Linus Torvalds with assistance from a loosely-knit team of hackers
	across the Net. It aims towards POSIX and <a class="reference external" href="http://www.unix.org/">Single UNIX Specification</a>
	compliance.</p>
	<p>It has all the features you would expect in a modern fully-fledged Unix,
	including true multitasking, virtual memory, shared libraries, demand
	loading, shared copy-on-write executables, proper memory management, and
	multistack networking including IPv4 and IPv6.</p>

*/
package main

import (
	"github.com/rbns/neinp"
	"github.com/rbns/neinp/fid"
	"github.com/rbns/neinp/fs"
	"github.com/rbns/neinp/message"
	"github.com/rbns/neinp/qid"
	"github.com/rbns/neinp/stat"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"
	"hash/fnv"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

var debug = true

func main() {
	flags := flag.NewFlagSet("rssfs", flag.ExitOnError)
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "%v [OPTIONS] URL [URL URL ...]\n", os.Args[0])
		flags.PrintDefaults()
	}
	addr := flags.String("addr", "localhost:9999", "listen address")
	uid := flags.String("uid", "nobody", "uid name")
	gid := flags.String("gid", "nogroup", "gid name")
	debug := flags.Bool("debug", false, "enable debug mode")
	flags.Parse(os.Args[1:])
	urls := flags.Args()

	urls, err := feedUrls(urls)
	if err != nil {
		log.Fatal(err)
	}

	l, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}

	r, err := New(*uid, *gid, urls)
	if err != nil {
		log.Fatal(err)
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			log.Fatal(err)
		}

		s := neinp.NewServer(r)
		s.Debug = *debug
		s.Serve(conn)
	}
}

func feedUrls(urls []string) ([]string, error) {
	fUrls := []string{}
	for _, v := range urls {
		fUrl, err := feedUrl(v)
		if err != nil {
			return fUrls, err
		}

		fUrls = append(fUrls, fUrl)
	}
	return fUrls, nil
}

func feedUrl(url string) (string, error) {
	if debug {
		log.Printf("Finding feed for %v", url)
	}
	res, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	ct := res.Header.Get("Content-Type")
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return "", err
	}

	switch mt {
	case "application/rss+xml", "application/atom+xml", "application/xml":
		if debug {
			log.Printf("url is feed (Content-Type: %v)", mt)
		}
		return url, nil
	case "text/html":
		if debug {
			log.Println("url is html")
		}
		return findFeed(res.Body)
	}

	return "", fmt.Errorf("no feed found: %v", url)
}

func findAttr(attrs []html.Attribute, key string) string {
	for _, v := range attrs {
		if v.Key == key {
			return v.Val
		}
	}

	return ""
}

func findFeed(r io.Reader) (string, error) {
	z := html.NewTokenizer(r)

	for {
		typ := z.Next()
		if typ == html.ErrorToken {
			break
		}
		if typ == html.StartTagToken || typ == html.SelfClosingTagToken {
			tok := z.Token()
			if tok.Data == "link" {
				linkRel := findAttr(tok.Attr, "rel")
				linkType := findAttr(tok.Attr, "type")
				linkHref := findAttr(tok.Attr, "href")
				if linkRel == "alternate" && (linkType == "application/rss+xml" || linkType == "application/atom+xml" || linkType == "application/xml") && linkHref != "" {
					if debug {
						log.Printf("link meta tag found: %v", linkHref)
					}
					return linkHref, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no rss link found")
}

func hashPath(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

func hashVersion(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

type RSSFs struct {
	neinp.NopP2000
	root fs.Entry
	fids *fid.Map
}

func New(uid, gid string, urls []string) (*RSSFs, error) {
	r := &RSSFs{}
	root, err := newRootDir(urls, uid, gid)
	r.root = root
	r.fids = fid.New()
	return r, err
}

type rootDir struct {
	*fs.Dir
}

func newRootDir(urls []string, uid, gid string) (*rootDir, error) {
	q := qid.Qid{Type: qid.TypeDir, Version: 0, Path: hashPath("/")}
	s := stat.Stat{
		Qid:    q,
		Mode:   0555 | stat.Dir,
		Atime:  time.Now(),
		Mtime:  time.Now(),
		Length: 0,
		Name:   "/",
		Uid:    uid,
		Gid:    gid,
		Muid:   uid,
	}

	children := []fs.Entry{}
	for _, v := range urls {
		f, err := newFeedDir(v, uid, gid)
		if err != nil {
			return nil, err
		}
		children = append(children, f)
	}

	r := &rootDir{
		Dir: fs.NewDir(s, children),
	}

	return r, nil
}

type feedDir struct {
	*fs.Dir
}

func newFeedDir(url, uid, gid string) (*feedDir, error) {
	fp := gofeed.NewParser()
	feed, err := fp.ParseURL(url)
	if err != nil {
		return nil, err
	}

	q := qid.Qid{Type: qid.TypeDir, Version: 0, Path: hashPath(url)}
	s := stat.Stat{
		Qid:    q,
		Mode:   0555 | stat.Dir,
		Atime:  time.Now(),
		Mtime:  time.Now(),
		Length: 0,
		Name:   feed.Title,
		Uid:    uid,
		Gid:    gid,
		Muid:   uid,
	}

	children := []fs.Entry{}
	for _, v := range feed.Items {
		item, err := newItemDir(v, uid, gid)
		if err != nil {
			return nil, err
		}
		children = append(children, item)
	}

	d := &feedDir{
		Dir: fs.NewDir(s, children),
	}

	return d, nil
}

type itemDir struct {
	*fs.Dir
}

func mediaUrl(url string) bool {
	mimeType := mime.TypeByExtension(path.Ext(url))
	return strings.HasPrefix(mimeType, "audio") || strings.HasPrefix(mimeType, "video")
}

func newItemDir(item *gofeed.Item, uid, gid string) (*itemDir, error) {
	q := qid.Qid{Type: qid.TypeDir, Version: uint32(time.Now().Unix()), Path: hashPath(item.Link)}
	s := stat.Stat{
		Qid:    q,
		Mode:   0555 | stat.Dir,
		Atime:  time.Now(),
		Mtime:  time.Now(),
		Length: 0,
		Name:   item.Title,
		Uid:    uid,
		Gid:    gid,
		Muid:   uid,
	}

	children := []fs.Entry{
		newStaticFile("title", q.Version, time.Now(), []byte(item.Title), uid, gid),
		newStaticFile("description", q.Version, time.Now(), []byte(item.Description), uid, gid),
		newStaticFile("content", q.Version, time.Now(), []byte(item.Content), uid, gid),
		newStaticFile("link", q.Version, time.Now(), []byte(item.Link), uid, gid),
		newStaticFile("guid", q.Version, time.Now(), []byte(item.GUID), uid, gid),
	}

	// if the GUID is an URL, use that as media source, else use enclosures
	if mediaUrl(item.GUID) {
		if debug {
			log.Printf("adding GUID %v as mediaFile", item.GUID)
		}

		name := path.Base(item.GUID)

		media, err := newMediaFile(name, q.Version, time.Now(), item.GUID, uid, gid)
		if err != nil {
			return nil, err
		}
		children = append(children, media)
	} else {
		for _, v := range item.Enclosures {
			if mediaUrl(v.URL) {
				if debug {
					log.Printf("adding enclosure %v as mediaFile", v.URL)
				}

				name := path.Base(v.URL)

				media, err := newMediaFile(name, q.Version, time.Now(), v.URL, uid, gid)
				if err != nil {
					return nil, err
				}
				children = append(children, media)
			}
		}
	}

	i := &itemDir{
		Dir: fs.NewDir(s, children),
	}

	return i, nil
}

type staticFile struct {
	*fs.File
}

func newStaticFile(name string, version uint32, times time.Time, data []byte, uid, gid string) *staticFile {
	q := qid.Qid{Type: qid.TypeFile, Version: version, Path: hashPath(name)}
	s := stat.Stat{
		Qid:    q,
		Mode:   0555,
		Atime:  times,
		Mtime:  times,
		Length: uint64(len(data)),
		Name:   name,
		Uid:    uid,
		Gid:    gid,
		Muid:   uid,
	}

	f := &staticFile{
		File: fs.NewFile(s, bytes.NewReader(data)),
	}

	return f
}

type mediaFile struct {
	*fs.File
	url  string
	stat stat.Stat
}

func newMediaFile(name string, version uint32, times time.Time, url, uid, gid string) (*mediaFile, error) {
	q := qid.Qid{Type: qid.TypeFile, Version: version, Path: hashPath(name)}
	s := stat.Stat{
		Qid:    q,
		Mode:   0555,
		Atime:  times,
		Mtime:  times,
		Length: 0,
		Name:   name,
		Uid:    uid,
		Gid:    gid,
		Muid:   uid,
	}

	f := &mediaFile{
		File: fs.NewFile(s, nil),
		stat: s,
		url:  url,
	}

	return f, nil
}

func (m *mediaFile) Stat() stat.Stat {
	return m.stat
}

func (m *mediaFile) Open() error {
	if debug {
		log.Printf("Opening %v", m.url)
	}

	// only download once
	if m.ReadSeeker == nil {
		res, err := http.Get(m.url)
		if err != nil {
			if debug {
				log.Println(err)
			}
			return err
		}
		defer res.Body.Close()

		m.stat.Length = uint64(res.ContentLength)

		buf, err := ioutil.ReadAll(res.Body)
		if err != nil {
			if debug {
				log.Println(err)
			}
			return err
		}

		m.ReadSeeker = bytes.NewReader(buf)
	}
	return nil
}

func (r *RSSFs) Version(ctx context.Context, m message.TVersion) (message.RVersion, error) {
	if !strings.HasPrefix(m.Version, "9P2000") {
		return message.RVersion{}, errors.New(message.BotchErrorString)
	}

	return message.RVersion{Version: "9P2000", Msize: m.Msize}, nil
}

func (r *RSSFs) Attach(ctx context.Context, m message.TAttach) (message.RAttach, error) {
	r.fids.Set(m.Fid, r.root)
	return message.RAttach{Qid: r.root.Qid()}, nil
}

func (r *RSSFs) Stat(ctx context.Context, m message.TStat) (message.RStat, error) {
	if e, ok := r.fids.Get(m.Fid).(fs.Entry); ok {
		return message.RStat{Stat: e.Stat()}, nil
	}
	return message.RStat{}, errors.New(message.NoStatErrorString)
}

func (r *RSSFs) Walk(ctx context.Context, m message.TWalk) (message.RWalk, error) {
	e, ok := r.fids.Get(m.Fid).(fs.Entry)
	if !ok {
		return message.RWalk{}, errors.New(message.NotFoundErrorString)
	}

	wqid := []qid.Qid{}

	wentry := e
	for _, v := range m.Wname {
		var err error
		wentry, err = wentry.Walk(v)
		if err != nil {
			return message.RWalk{}, err
		}

		q := wentry.Qid()

		wqid = append(wqid, q)
	}

	if len(m.Wname) == len(wqid) {
		r.fids.Set(m.Newfid, wentry)
	}

	return message.RWalk{Wqid: wqid}, nil
}

func (r *RSSFs) Open(ctx context.Context, m message.TOpen) (message.ROpen, error) {
	e, ok := r.fids.Get(m.Fid).(fs.Entry)
	if !ok {
		return message.ROpen{}, errors.New(message.UnknownFidErrorString)
	}

	q := e.Qid()
	if err := e.Open(); err != nil {
		return message.ROpen{}, errors.New(message.BotchErrorString)
	}

	return message.ROpen{Qid: q}, nil
}

func (r *RSSFs) Read(ctx context.Context, m message.TRead) (message.RRead, error) {
	e, ok := r.fids.Get(m.Fid).(fs.Entry)
	if !ok {
		return message.RRead{}, errors.New(message.UnknownFidErrorString)
	}

	_, err := e.Seek(int64(m.Offset), io.SeekStart)
	if err != nil {
		return message.RRead{}, err
	}

	buf := make([]byte, m.Count)
	n, err := e.Read(buf)
	if err != nil && err != io.EOF {
		return message.RRead{}, err
	}

	return message.RRead{Count: uint32(n), Data: buf[:n]}, nil
}

func (r *RSSFs) Clunk(ctx context.Context, m message.TClunk) (message.RClunk, error) {
	r.fids.Delete(m.Fid)
	return message.RClunk{}, nil
}
