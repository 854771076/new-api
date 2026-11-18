import { expect, mock, test } from 'bun:test';
mock.module('server-only',()=>({}));
mock.module('@/lib/gateway',()=>({fetchGatewayPricing:async()=>{throw new Error('Not used')}}));
const {estimateImageQuota,estimateVisionQuota,quotePricingVersion}=await import('../pricing');
const pricing=(expr)=>({data:[{model_name:'image',billing_mode:'tiered_expr',billing_expr:expr,enable_groups:['default']}],group_ratio:{default:1},quota_per_unit:500000});
test('estimates exact fixed and linear prices, including explicitly free image inputs',()=>{
 expect(estimateImageQuota(pricing('v1:tier("base", fixed(0.01))'),'image')).toBe(5000);
 expect(estimateImageQuota(pricing('tier("base", p * 2 + img * 0 + c * 8)'),'image')).toBe(5508);
 expect(estimateVisionQuota(pricing('tier("base", fixed(0))'),'image')).toBe(0);
});
test.each(['len > 100 ? tier("x", fixed(1)) : tier("y", fixed(2))','tier("base", fixed(1)) * image_count','p * 1 - c * 3','p * 1 + u("seconds") * 9','v2:p * 1'])('unsupported expression %s never silently uses a legacy free price',expr=>{
 expect(()=>estimateImageQuota(pricing(expr),'image')).toThrow();
});
test('missing models and unknown group prices fail closed',()=>{
 expect(()=>estimateImageQuota(pricing('p * 1'),'image-latest')).toThrow();
 expect(()=>estimateImageQuota({...pricing('p * 1'),group_ratio:{}},'image')).toThrow();
});
test('legacy token quotes include input and completion multipliers',()=>{
 const value=pricing('');value.data[0]={model_name:'image',model_ratio:2,completion_ratio:3,image_ratio:0,quota_type:0};
 expect(estimateImageQuota(value,'image')).toBe(8312);
});
test('quote pricing version is stable across response key and catalog order',()=>{
 const first=pricing('p * 1');first.data.push({model_name:'vision',billing_expr:'p * 2'});first.group_ratio={default:1,other:2};
 const second={...first,data:[...first.data].reverse().map(entry=>Object.fromEntries(Object.entries(entry).reverse())),group_ratio:{other:2,default:1}};
 expect(quotePricingVersion(first,'image','vision')).toBe(quotePricingVersion(second,'image','vision'));
 second.quota_per_credit=100;
 expect(quotePricingVersion(first,'image','vision')).not.toBe(quotePricingVersion(second,'image','vision'));
});
