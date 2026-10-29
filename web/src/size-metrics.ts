// Byte values remain canonical decimal strings; display rounding never changes the source value.
export type SizeUnit = 'B' | 'GB' | 'TB' | 'GiB' | 'TiB';
const divisors: Record<SizeUnit,bigint> = {B:1n,GB:1000n**3n,TB:1000n**4n,GiB:1024n**3n,TiB:1024n**4n};

// exactBytes distinguishes unknown values and rejects already-rounded JavaScript numbers.
export function exactBytes(value:unknown):bigint|null {
 if(value===null)return null;
 if(typeof value!=='string'||value.length>128||!/^(0|[1-9][0-9]*)$/.test(value))throw new Error('INVALID_BYTES');
 return BigInt(value);
}

// byteUnitDecimal produces a terminating exact decimal so switching units can round-trip losslessly.
export function byteUnitDecimal(value:unknown,unit:SizeUnit):string|null {
 const bytes=exactBytes(value);if(bytes===null)return null;const divisor=divisors[unit];if(!divisor)throw new Error('INVALID_UNIT');
 const whole=bytes/divisor;let rest=bytes%divisor;let fraction='';
 while(rest!==0n){rest*=10n;fraction+=(rest/divisor).toString();rest%=divisor;}
 return whole.toString()+(fraction?'.'+fraction:'');
}

// unitDecimalBytes accepts only exact integral-byte quantities, never rounded approximations.
export function unitDecimalBytes(value:string,unit:SizeUnit):string {
 if(value.length>180||!/^(0|[1-9][0-9]*)(\.[0-9]+)?$/.test(value)||!divisors[unit])throw new Error('INVALID_UNIT_VALUE');
 const [whole,fraction='']=value.split('.');const scale=10n**BigInt(fraction.length);const numerator=BigInt(whole+fraction)*divisors[unit];
 if(numerator%scale!==0n)throw new Error('FRACTIONAL_BYTE');const bytes=(numerator/scale).toString();exactBytes(bytes);return bytes;
}

// formatByteSize rounds only the label; exactBytes is retained for details and subsequent conversions.
export function formatByteSize(value:unknown,unit:SizeUnit,decimals=2):{label:string|null;exactBytes:string|null;unit:SizeUnit} {
 const bytes=exactBytes(value);if(!Number.isInteger(decimals)||decimals<0||decimals>6||!divisors[unit])throw new Error('INVALID_FORMAT');
 if(bytes===null)return {label:null,exactBytes:null,unit};
 const scale=10n**BigInt(decimals);const divisor=divisors[unit];const rounded=(bytes*scale+divisor/2n)/divisor;
 const text=decimals===0?rounded.toString():(rounded/scale).toString()+'.'+(rounded%scale).toString().padStart(decimals,'0');
 return {label:text+' '+unit,exactBytes:bytes.toString(),unit};
}
