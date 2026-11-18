import { expect, test } from 'bun:test';
import { createAppProxyClient } from './index';

test('proxy SDK fixes App ownership, preserves request IDs and keeps the admin credential out of relay', async () => {
  const requests: {url:URL; options:RequestInit}[] = [];
  const client = createAppProxyClient({baseUrl:'https://gateway.example', appId:7, accessToken:'nap_test', fetch:async (input,options) => {
    requests.push({url:new URL(String(input)),options:options!});
    return Response.json({success:true,data:{user_id:42,app_id:7}});
  }});
  await client.provision('https://native.example/auth/v1','native-user');
  await client.createKey(42,{unlimited_quota:true},'issue-once-0001');
  await client.checkout(42,{purchase_type:'topup',payment_method:'waffo_pancake',amount:10},'checkout-once-0001');
  expect(requests.every(request=>request.url.searchParams.get('app_id')==='7')).toBe(true);
  expect(requests.every(request=>request.options.redirect==='error' && !request.url.pathname.startsWith('/v1'))).toBe(true);
  expect(JSON.parse(String(requests[2].options.body)).app_id).toBe(7);
  expect(new Headers(requests[2].options.headers).get('Idempotency-Key')).toBe('checkout-once-0001');
  expect(()=>client.order(42,'..')).toThrow('Invalid order ID');
  expect(()=>client.wallet(0)).toThrow();
  expect(()=>createAppProxyClient({baseUrl:'http://gateway.example',appId:7,accessToken:'nap_test'})).toThrow();
});

test('proxy pricing retains conversion and group metadata with the price list',async()=>{
  const pricing={success:true,data:[],app_id:7,quota_per_credit:100,group_ratio:{default:1}};
  const client=createAppProxyClient({baseUrl:'https://gateway.example',appId:7,accessToken:'nap_test',fetch:async()=>Response.json(pricing)});
  expect(await client.pricing(42)).toEqual(pricing);
});

test('proxy SDK rejects failed management responses',async()=>{
  const client=createAppProxyClient({baseUrl:'https://gateway.example',appId:7,accessToken:'nap_test',fetch:async()=>Response.json({success:false,message:'rejected'})});
  await expect(client.wallet(42)).rejects.toThrow('App proxy request failed');
});
