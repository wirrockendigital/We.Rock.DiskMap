export const MAX_CANVAS_PIXELS=16777216;
// Bound backing memory to 64 MiB RGBA while preserving actual scale after pixel rounding.
export function canvasGeometry(width:number,height:number,dpr:number) {
 if(![width,height,dpr].every(Number.isFinite)||width<0||height<0||width>16384||height>16384||dpr<=0)throw Error('INVALID_CANVAS_SIZE');
 const scale=Math.min(dpr,4,width&&height?Math.sqrt(MAX_CANVAS_PIXELS/(width*height)):1);
 const pixelWidth=Math.floor(width*scale),pixelHeight=Math.floor(height*scale);
 return Object.freeze({width,height,pixelWidth,pixelHeight,scaleX:width?pixelWidth/width:1,scaleY:height?pixelHeight/height:1,estimatedBytes:pixelWidth*pixelHeight*4,limited:scale<dpr});
}
// Setting dimensions resets context state; apply a fresh transform instead of multiplying old scales.
export function configureCanvas(canvas:HTMLCanvasElement,width:number,height:number,dpr:number) {
 const geometry=canvasGeometry(width,height,dpr);
 canvas.width=geometry.pixelWidth;canvas.height=geometry.pixelHeight;
 canvas.style.width=`${width}px`;canvas.style.height=`${height}px`;
 const context=canvas.getContext('2d');if(!context)throw Error('CANVAS_UNAVAILABLE');
 context.setTransform(geometry.scaleX,0,0,geometry.scaleY,0,0);
 return geometry;
}
