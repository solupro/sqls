package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/sourcegraph/jsonrpc2"
	"github.com/urfave/cli/v2"

	"github.com/lighttiger2505/sqls/internal/config"
	"github.com/lighttiger2505/sqls/internal/handler"
)

// builtin variables. see Makefile
var (
	version  string
	revision string
)

func main() {
	if err := realMain(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}

func realMain() error {
	app := &cli.App{
		Name:    "sqls",
		Version: fmt.Sprintf("Version:%s, Revision:%s\n", version, revision),
		Usage:   "An implementation of the Language Server Protocol for SQL.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "log",
				Aliases: []string{"l"},
				Usage:   "Also log to this file. (in addition to stderr)",
			},
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "Specifies an alternative per-user configuration file. If a configuration file is given on the command line, the workspace option (initializationOptions) will be ignored.",
			},
			&cli.BoolFlag{
				Name:    "trace",
				Aliases: []string{"t"},
				Usage:   "Print all requests and responses.",
			},
		},
		Commands: cli.Commands{
			{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "edit config",
				Action: func(c *cli.Context) error {
					editorEnv := os.Getenv("EDITOR")
					if editorEnv == "" {
						editorEnv = "vim"
					}
					return OpenEditor(editorEnv, config.YamlConfigPath)
				},
			},
		},
		Action: func(c *cli.Context) error {
			return serve(c)
		},
	}
	cli.VersionFlag = &cli.BoolFlag{
		Name:    "version",
		Aliases: []string{"v"},
		Usage:   "Print version.",
	}
	cli.HelpFlag = &cli.BoolFlag{
		Name:    "help",
		Aliases: []string{"h"},
		Usage:   "Print help.",
	}

	err := app.Run(os.Args)
	if err != nil {
		return err
	}

	return nil
}

func serve(c *cli.Context) error {
	logfile := c.String("log")

	// Initialize log writer
	var logWriter io.Writer
	if logfile != "" {
		f, err := os.OpenFile(logfile, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0660)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		logWriter = io.MultiWriter(os.Stderr, f)
	} else {
		logWriter = io.MultiWriter(os.Stderr)
	}
	log.SetOutput(logWriter)

	// websocket server
	addr := ":8091"
	http.HandleFunc("/sqls", wsServe)
	log.Println("sqls websocket server on:", addr)
	err := http.ListenAndServe(addr, nil)
	if nil != err {
		log.Fatal("server error:", err)
	}

	return nil
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func wsServe(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	defer conn.Close()

	wsHandleRequest(conn)
	wsClose(conn)
	log.Println("Exiting wsServe")
}

func wsHandleRequest(ws *websocket.Conn) {
	// Initialize language server
	server := handler.NewServer()
	h := jsonrpc2.HandlerWithError(server.Handle)
	ctx := context.Background()
	conn := jsonrpc2.NewConn(
		ctx,
		jsonrpc2.NewBufferedStream(ws.UnderlyingConn(), jsonrpc2.VSCodeObjectCodec{}),
		h,
	)

	defer func() {
		if err := conn.Close(); err != nil {
			log.Println(err)
		}

		if err := server.Stop(); err != nil {
			log.Println(err)
		}
	}()

	for {
		mt, datas, err := ws.ReadMessage()
		if nil != err {
			log.Println("reader error:", err)
			return
		}
		var req jsonrpc2.Request
		err = json.Unmarshal(datas, &req)
		if nil != err {
			log.Println("parse request error:", err)
			continue
		}

		result, err := server.Handle(ctx, conn, &req)
		if nil != err {
			log.Println("call method error:", err)
		}
		res, err := json.Marshal(result)
		err = ws.WriteMessage(mt, res)
		if nil != err {
			log.Println("writer error:", err)
			return
		}
	}
}
func wsClose(ws *websocket.Conn) error {
	const deadline = time.Second
	return ws.WriteControl(websocket.CloseMessage, []byte{}, time.Now().Add(deadline))
}

func OpenEditor(program string, args ...string) error {
	cmdargs := strings.Join(args, " ")
	command := program + " " + cmdargs

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
