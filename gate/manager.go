package gate

import (
	"sync"

	"github.com/juju/errors"
	"golang.org/x/net/context"

	"github.com/dearcode/candy/meta"
	"github.com/dearcode/candy/util"
	"github.com/dearcode/candy/util/log"
)

// manager 管理所有session, connection.
type manager struct {
	stop     bool
	host     string
	notifer  *util.NotiferClient
	conns    map[string]*connection // 所有的connection
	sessions map[int64]*session     // 在线用户
	pushChan chan meta.PushRequest
	sync.RWMutex
}

func newManager(n *util.NotiferClient, host string) *manager {
	m := &manager{
		notifer:  n,
		host:     host,
		sessions: make(map[int64]*session),
		conns:    make(map[string]*connection),
		pushChan: make(chan meta.PushRequest, 1000),
	}

	return m
}

func (m *manager) online(id int64, device string, c *connection) {
	log.Debugf("user:%d, conn:%+v, device:%s", id, c, device)
	c.setDevice(device)

	m.Lock()
	s, ok := m.sessions[id]
	if !ok {
		s = newSession(id, c)
		m.sessions[id] = s
	}

	s.addConnection(c)

	m.conns[c.getAddr()] = c

	m.Unlock()

	c.setUser(id)

	//订阅消息
	sid, err := m.notifer.Subscribe(id, device, m.host)
	if err != nil {
		log.Errorf("%d dev:%s Subscribe error:%s", id, device, errors.ErrorStack(err))
		return
	}
	s.setSubscribeID(sid)
}

func (m *manager) offline(id int64, c *connection) {
	log.Debugf("user:%d, conn:%+v", id, c)

	m.Lock()
	s, ok := m.sessions[id]
	if ok {
		s.delConnection(c)
		delete(m.sessions, id)
	}

	delete(m.conns, c.getAddr())

	m.Unlock()

	//消息订阅
	if err := m.notifer.UnSubscribe(id, c.getDevice(), m.host, s.getSubscribeID()); err != nil {
		log.Errorf("UnSubscribe error:%s", errors.ErrorStack(err))
	}
}

func (m *manager) getConnection(ctx context.Context) (c *connection, ok bool, err error) {
	var addr string

	if addr, err = util.ContextAddr(ctx); err != nil {
		return
	}
	log.Debugf("conn from:%s, conns:%+v", addr, m.conns)
	m.Lock()
	if c, ok = m.conns[addr]; !ok {
		c = newConnection(addr)
		m.conns[addr] = c
		log.Debugf("conn from:%s, c:%+v", addr, c)
	}
	m.Unlock()
	return
}

func (m *manager) getUserSession(id int64) *session {
	m.RLock()
	s, ok := m.sessions[id]
	m.RUnlock()
	if !ok {
		return nil
	}

	return s
}

func (m *manager) getSession(ctx context.Context) (*session, *connection, error) {
	c, ok, err := m.getConnection(ctx)
	if err != nil {
		return nil, nil, errors.Trace(err)
	}

	if !ok || c.getUser() == 0 {
		return nil, c, errors.Trace(util.ErrInvalidContext)
	}

	s := m.getUserSession(c.getUser())
	if s == nil {
		return nil, c, errors.Trace(util.ErrInvalidContext)
	}

	return s, c, nil
}

func (m *manager) pushMessage(req *meta.PushRequest) {
	for _, id := range req.ID {
		s := m.getUserSession(id.User)
		if s == nil {
			continue
		}
		s.walkConnection(func(c *connection) bool {
			if err := c.send(&req.Msg); err != nil {
				log.Errorf("send Msg:%+v, to session:%+v, err:%s", req.Msg, s, errors.ErrorStack(err))
			}
			return false
		})
	}
}
