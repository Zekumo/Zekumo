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
  reply(type, data) { this.emit('message', { data: JSON.stringify({ type, data }) }); }
}
test('room SDK serializes options and consumes dedicated responses', async () => {
  const sdk = new Zekumo({appId:'a',baseUrl:'https://example.test',token:'test',webSocket:Socket});
  const connecting = sdk.realtime.connect(); const socket = Socket.latest;
  socket.emit('open', {}); await connecting;
  try {
    let pending = sdk.realtime.listRoomsPage({offset:20,limit:10});
    assert.deepEqual(socket.sent.at(-1),{type:'room.list',data:{offset:20,limit:10}});
    socket.reply('room.list',{rooms:[],offset:20,limit:10,total:20});
    assert.equal((await pending).total,20);
    pending = sdk.realtime.updateRoom({locked:false,meta:null});
    assert.deepEqual(socket.sent.at(-1),{type:'room.update',data:{locked:false,meta:null}});
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
