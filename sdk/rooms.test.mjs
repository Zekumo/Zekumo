import test from 'node:test';
import assert from 'node:assert/strict';
import { Zekumo } from './dist/esm/index.js';
class Socket {
  static latest;
  readyState = 1;
  listeners = new Map();
  sent = [];
  constructor() { Socket.latest = this; }
  addEventListener(type, fn) { this.listeners.set(type, fn); }
  send(raw) { this.sent.push(JSON.parse(raw)); }
  close() {}
  emit(type, data) { this.listeners.get(type)?.(data); }
  reply(type, data, request_id = this.sent.at(-1)?.request_id) { this.emit('message', { data: JSON.stringify({ type, data, request_id }) }); }
}
test('room SDK serializes options and consumes dedicated responses', async () => {
  const sdk = new Zekumo({appId:'a',baseUrl:'https://example.test',token:'test',webSocket:Socket});
  const connecting = sdk.realtime.connect(); const socket = Socket.latest;
  socket.emit('open', {}); await connecting;
  try {
    let pending = sdk.realtime.listRoomsPage({offset:20,limit:10});
    assert.deepEqual(socket.sent.at(-1),{type:'room.list',data:{offset:20,limit:10},request_id:'r1'});
    socket.reply('room.list',{rooms:[],offset:20,limit:10,total:20});
    assert.equal((await pending).total,20);
    pending = sdk.realtime.updateRoom({locked:false,meta:null});
    assert.deepEqual(socket.sent.at(-1),{type:'room.update',data:{locked:false,meta:null},request_id:'r2'});
    socket.reply('room.updated',{id:'r'}); assert.equal((await pending).id,'r');
    pending = sdk.realtime.kickRoomMember('p');
    assert.equal(socket.sent.at(-1).type,'room.kick');
    socket.reply('room.kick_ok',{player_id:'p',room_id:'r'}); await pending;
    pending = sdk.realtime.transferRoom('p');
    socket.reply('room.transferred',{id:'r',owner_id:'p'}); assert.equal((await pending).owner_id,'p');
    pending = sdk.realtime.getRoom();
    socket.reply('room.info',{id:'r',members:[]}); assert.equal((await pending).id,'r');
  } finally { sdk.realtime.close(); }
});

async function connection() {
  const sdk = new Zekumo({appId:'a',baseUrl:'https://example.test',token:'test',webSocket:Socket});
  const opened = sdk.realtime.connect();
  const socket = Socket.latest;
  socket.emit('open', {}); await opened;
  return {sdk, socket};
}

test('concurrent same-type requests resolve by ID even out of order', async () => {
  const {sdk, socket} = await connection();
  try {
    const first = sdk.realtime.listRoomsPage({offset:0});
    const second = sdk.realtime.listRoomsPage({offset:20});
    const [a,b] = socket.sent;
    assert.notEqual(a.request_id,b.request_id);
    socket.reply('room.list',{offset:20,rooms:[]}, b.request_id);
    socket.reply('room.list',{offset:0,rooms:[]}, a.request_id);
    assert.equal((await first).offset,0);
    assert.equal((await second).offset,20);
  } finally { sdk.realtime.close(); }
});

test('broadcasts and unrelated errors do not settle pending requests', async () => {
  const {sdk, socket} = await connection();
  try {
    let settled = false;
    const request = sdk.realtime.updateRoom({name:'mine'}).then(x => {settled=true;return x;});
    const id = socket.sent.at(-1).request_id;
    socket.reply('room.updated',{name:'broadcast'}, null);
    socket.reply('error',{code:'other_error'}, 'unrelated');
    socket.reply('error',{code:'uncorrelated'}, null);
    await Promise.resolve(); assert.equal(settled,false);
    socket.reply('room.updated',{name:'mine'}, id);
    assert.equal((await request).name,'mine');
  } finally { sdk.realtime.close(); }
});

test('correlated errors reject only their request and mismatched types fail', async () => {
  const {sdk, socket} = await connection();
  try {
    const a=sdk.realtime.getRoom(), b=sdk.realtime.listRoomsPage();
    const [ra,rb]=socket.sent;
    const rejected=assert.rejects(a,e=>e.code==='not_in_room');
    socket.reply('error',{code:'not_in_room',message:'join first'},ra.request_id);
    socket.reply('room.list',{rooms:[]},rb.request_id);
    await rejected; assert.deepEqual((await b).rooms,[]);
    const bad=sdk.realtime.getRoom();
    const mismatch=assert.rejects(bad,e=>e.code==='unexpected_reply');
    socket.reply('room.list',{}); await mismatch;
  } finally { sdk.realtime.close(); }
});

test('disconnect immediately rejects every pending request without replay', async () => {
  const {sdk, socket} = await connection();
  const pending=[sdk.realtime.getRoom(),sdk.realtime.listRoomsPage()];
  const rejected=pending.map(p=>assert.rejects(p,e=>e.code==='ws_closed'));
  sdk.realtime.close();
  await Promise.all(rejected);
  socket.reply('room.info',{id:'late'},socket.sent[0].request_id);
  assert.equal(socket.sent.length,2);
});

test('pending requests are bounded and capacity is released after replies', async () => {
  const {sdk, socket} = await connection();
  try {
    const pending=Array.from({length:64},()=>sdk.realtime.getRoom());
    await assert.rejects(sdk.realtime.getRoom(),e=>e.code==='too_many_requests');
    assert.equal(socket.sent.length,64);
    for (const request of socket.sent) socket.reply('room.info',{id:request.request_id},request.request_id);
    await Promise.all(pending);
    const next=sdk.realtime.getRoom(); socket.reply('room.info',{id:'next'});
    assert.equal((await next).id,'next');
  } finally { sdk.realtime.close(); }
});

test('failed sends do not leak pending capacity', async () => {
  const {sdk, socket} = await connection();
  try {
    socket.send=()=>{throw new Error('send failed');};
    for(let i=0;i<65;i++) await assert.rejects(sdk.realtime.getRoom(),/send failed/);
  } finally { sdk.realtime.close(); }
});

test('messages from a retired socket cannot settle a new connection request', async () => {
  const {sdk, socket:old} = await connection();
  sdk.realtime.close();
  const opening=sdk.realtime.connect(), current=Socket.latest;
  current.emit('open',{}); await opening;
  try {
    let settled=false;
    const pending=sdk.realtime.getRoom().then(x=>{settled=true;return x;});
    const id=current.sent.at(-1).request_id;
    old.reply('room.info',{id:'retired'},id);
    await Promise.resolve(); assert.equal(settled,false);
    current.reply('room.info',{id:'current'},id);
    assert.equal((await pending).id,'current');
  } finally {sdk.realtime.close();}
});

test('server close rejects pending requests and malformed frames are ignored', async () => {
  const {sdk,socket}=await connection();
  const pending=sdk.realtime.getRoom();
  const rejected=assert.rejects(pending,e=>e.code==='ws_closed');
  for(const data of ['null','{','[]','{"type":123}']) socket.emit('message',{data});
  socket.readyState=3;
  socket.emit('close',{code:1008,reason:'replaced'});
  await rejected;
  sdk.realtime.close();
});

test('timed-out requests release pending capacity and ignore late replies', async (t) => {
  t.mock.timers.enable({apis:['setTimeout']});
  const {sdk,socket}=await connection();
  try {
    const pending=sdk.realtime.getRoom();
    const id=socket.sent.at(-1).request_id;
    const rejected=assert.rejects(pending,e=>e.code==='timeout');
    t.mock.timers.tick(10001); await rejected;
    socket.reply('room.info',{id:'late'},id);
    const next=sdk.realtime.getRoom();socket.reply('room.info',{id:'new'});
    assert.equal((await next).id,'new');
  } finally {sdk.realtime.close();}
});

test('closing an unfinished connection rejects it and permits a fresh connect', async () => {
  const sdk=new Zekumo({appId:'a',baseUrl:'https://example.test',token:'test',webSocket:Socket});
  const first=sdk.realtime.connect(); const old=Socket.latest;
  const rejected=assert.rejects(first,e=>e.code==='ws_closed');
  sdk.realtime.close(); await rejected;
  const second=sdk.realtime.connect(); const current=Socket.latest;
  assert.notEqual(current,old);
  old.emit('open',{});
  current.emit('open',{}); await second;
  sdk.realtime.close();
});

test('replacement before opening rejects connect instead of leaving it pending', async () => {
  const sdk=new Zekumo({appId:'a',baseUrl:'https://example.test',token:'test',webSocket:Socket});
  const opening=sdk.realtime.connect();
  const rejected=assert.rejects(opening,e=>e.code==='ws_closed');
  Socket.latest.readyState=3;
  Socket.latest.emit('close',{code:1008,reason:'replaced'});
  await rejected; sdk.realtime.close();
});

test('manual connect during backoff owns the socket and preserves correlated replies', async (t) => {
  t.mock.timers.enable({apis:['setTimeout']});
  const {sdk,socket:old}=await connection();
  try {
    old.readyState=3; old.emit('close',{code:1006});
    const opening=sdk.realtime.connect(), current=Socket.latest;
    current.emit('open',{}); await opening;
    const pending=sdk.realtime.getRoom();
    t.mock.timers.tick(1001);
    assert.equal(Socket.latest,current);
    assert.equal(sdk.realtime.connected,true);
    current.reply('room.info',{id:'still-current'});
    assert.equal((await pending).id,'still-current');
  } finally {sdk.realtime.close();}
});

test('automatic reconnect shares its pending opening promise with manual connect', async (t) => {
  t.mock.timers.enable({apis:['setTimeout']});
  const {sdk,socket:old}=await connection();
  try {
    old.readyState=3; old.emit('close',{code:1006});
    t.mock.timers.tick(1001);
    const current=Socket.latest; current.readyState=0;
    const opening=sdk.realtime.connect();
    assert.equal(Socket.latest,current);
    current.readyState=1; current.emit('open',{}); await opening;
    assert.equal(sdk.realtime.connected,true);
  } finally {sdk.realtime.close();}
});

test('reconnecting listener may close or connect without leaving an orphan timer', async (t) => {
  t.mock.timers.enable({apis:['setTimeout']});
  for (const action of ['close','connect']) {
    const {sdk,socket:old}=await connection();
    let opening;
    sdk.realtime.on('reconnecting',()=>{
      if(action==='close') sdk.realtime.close();
      else opening=sdk.realtime.connect();
    });
    try {
      old.readyState=3; old.emit('close',{code:1006});
      const current=Socket.latest;
      if(opening) {current.emit('open',{}); await opening;}
      t.mock.timers.tick(1001);
      assert.equal(Socket.latest,current);
      assert.equal(sdk.realtime.connected,action==='connect');
    } finally {sdk.realtime.close();}
  }
});
