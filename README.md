# rssfs
[![GoDoc](https://godoc.org/github.com/rbns/rssfs?status.svg)](https://godoc.org/github.com/rbns/rssfs)

## about
rssfs is a 9p server serving the contents of rss feeds. it's main purpose is being an example of how to
use my [neinp](https://github.com/rbns/neinp) go package.

## installation

	go get github.com/rbns/rssfs

## usage
./rssfs [OPTIONS] URL [URL URL ...]
  -addr string
    	listen address (default "localhost:9999")
  -debug
    	enable debug mode
  -gid string
    	gid name (default "nogroup")
  -uid string
    	uid name (default "nobody")

### example

	$ ./rssfs https://www.kernel.org
	$ mount -t9p -o port=9999,noextend 127.0.0.1 /mnt/tmp
	$ tree /mnt/tmp/ | head -14
	/mnt/tmp/
	└── The Linux Kernel Archives
		├── About Linux Kernel
		│   ├── content
		│   ├── description
		│   ├── guid
		│   ├── link
		│   └── title
		├── Active kernel releases
		│   ├── content
		│   ├── description
		│   ├── guid
		│   ├── link
		│   └── title
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

