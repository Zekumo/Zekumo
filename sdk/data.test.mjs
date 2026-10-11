import test from 'node:test';
import assert from 'node:assert/strict';
import { Zekumo } from './dist/esm/index.js';
const revision='12345678-1234-4234-8234-123456789abc';
function fixture(status=200,body={key:'save',value:{level:3},revision}) {
  const requests=[];
  const sdk=new Zekumo({appId:'a',baseUrl:'https://game.example',token:'player-token',
    fetch:async(url,options)=>{requests.push({url,...options});return new Response(JSON.stringify(body),{status});}});
  return {sdk,requests};
}
test('cloud-save reads retain value-only compatibility and expose revisions',async()=>{
  const {sdk,requests}=fixture();
  assert.deepEqual(await sdk.playerData.get('a/b'),{level:3});
  assert.equal((await sdk.playerData.getEntry('a/b')).revision,revision);
  assert.equal(requests[0].url,'https://game.example/v1/player/data/a%2Fb');
  assert.equal(requests[0].headers.Authorization,'Bearer player-token');
});
test('cloud-save conditional writes send strong revisions without wrapping JSON',async()=>{
  const {sdk,requests}=fixture();
  await sdk.playerData.set('save',{level:4},{expectedRevision:revision});
  assert.equal(requests[0].headers['If-Match'],`"${revision}"`);
  assert.equal(requests[0].body,'{"level":4}');
  await sdk.playerData.set('save',null,{createOnly:true});
  assert.equal(requests[1].headers['If-None-Match'],'*');assert.equal(requests[1].body,'null');
  await sdk.playerData.delete('save',{expectedRevision:revision});
  assert.equal(requests[2].method,'DELETE');assert.equal(requests[2].headers['If-Match'],`"${revision}"`);
  await sdk.playerData.set('save',true,{expectedRevision:'*'});
  assert.equal(requests[3].headers['If-Match'],'*');
  await sdk.playerData.set('save',1);assert.equal(requests[4].headers['If-Match'],undefined);
});
test('cloud-save conflicts surface typed 412 errors without retries',async()=>{
  const {sdk,requests}=fixture(412,{error:{code:'save_conflict',message:'reload'}});
  await assert.rejects(sdk.playerData.set('save',true,{expectedRevision:revision}),e=>e.status===412&&e.code==='save_conflict');
  assert.equal(requests.length,1);
});
test('invalid save conditions never fall back to an unconditional request',async()=>{
  const {sdk,requests}=fixture();
  for(const expectedRevision of ['',`"${revision}"`,'bad','x\r\nInjected: yes']) {
    await assert.rejects(sdk.playerData.set('save',true,{expectedRevision}),e=>e.code==='bad_options');
  }
  await assert.rejects(sdk.playerData.set('save',true,{expectedRevision:revision,createOnly:true}),e=>e.code==='bad_options');
  assert.equal(requests.length,0);
});
