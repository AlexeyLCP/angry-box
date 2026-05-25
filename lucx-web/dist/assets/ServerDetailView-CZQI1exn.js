const __vite__mapDeps=(i,m=__vite__mapDeps,d=(m.f||(m.f=["assets/client-Bc7kWmUb.js","assets/index-DuSDhoyn.js","assets/index-CjxcgXAc.js","assets/index-Cku9uws5.css"])))=>i.map(i=>d[i]);
import{a as L,ar as u,a2 as p,aj as T,a6 as D,aM as E,ap as V,Z as e,aF as o,am as X,a5 as i,aQ as f,aI as l,a0 as B,a1 as b,aK as H,av as A,ao as $,g as N,aw as j,_ as I,ay as O,t as U}from"./index-CjxcgXAc.js";import{u as F}from"./servers-D0jUgP1f.js";import{u as K}from"./chains-nU2J9h5d.js";import{s as g}from"./index-ClcFD60b.js";import{s as _}from"./index-CHWju6vN.js";import{s as M,f as J}from"./index-DuSDhoyn.js";import"./client-Bc7kWmUb.js";import"./index-Dh1e4SK4.js";var Q=`
    .p-skeleton {
        display: block;
        overflow: hidden;
        background: dt('skeleton.background');
        border-radius: dt('skeleton.border.radius');
    }

    .p-skeleton::after {
        content: '';
        animation: p-skeleton-animation 1.2s infinite;
        height: 100%;
        left: 0;
        position: absolute;
        right: 0;
        top: 0;
        transform: translateX(-100%);
        z-index: 1;
        background: linear-gradient(90deg, rgba(255, 255, 255, 0), dt('skeleton.animation.background'), rgba(255, 255, 255, 0));
    }

    [dir='rtl'] .p-skeleton::after {
        animation-name: p-skeleton-animation-rtl;
    }

    .p-skeleton-circle {
        border-radius: 50%;
    }

    .p-skeleton-animation-none::after {
        animation: none;
    }

    @keyframes p-skeleton-animation {
        from {
            transform: translateX(-100%);
        }
        to {
            transform: translateX(100%);
        }
    }

    @keyframes p-skeleton-animation-rtl {
        from {
            transform: translateX(100%);
        }
        to {
            transform: translateX(-100%);
        }
    }
`,Z={root:{position:"relative"}},q={root:function(s){var r=s.props;return["p-skeleton p-component",{"p-skeleton-circle":r.shape==="circle","p-skeleton-animation-none":r.animation==="none"}]}},G=L.extend({name:"skeleton",style:Q,classes:q,inlineStyles:Z}),W={name:"BaseSkeleton",extends:M,props:{shape:{type:String,default:"rectangle"},size:{type:String,default:null},width:{type:String,default:"100%"},height:{type:String,default:"1rem"},borderRadius:{type:String,default:null},animation:{type:String,default:"wave"}},style:G,provide:function(){return{$pcSkeleton:this,$parentInstance:this}}};function k(n){"@babel/helpers - typeof";return k=typeof Symbol=="function"&&typeof Symbol.iterator=="symbol"?function(s){return typeof s}:function(s){return s&&typeof Symbol=="function"&&s.constructor===Symbol&&s!==Symbol.prototype?"symbol":typeof s},k(n)}function Y(n,s,r){return(s=ee(s))in n?Object.defineProperty(n,s,{value:r,enumerable:!0,configurable:!0,writable:!0}):n[s]=r,n}function ee(n){var s=te(n,"string");return k(s)=="symbol"?s:s+""}function te(n,s){if(k(n)!="object"||!n)return n;var r=n[Symbol.toPrimitive];if(r!==void 0){var y=r.call(n,s);if(k(y)!="object")return y;throw new TypeError("@@toPrimitive must return a primitive value.")}return(s==="string"?String:Number)(n)}var w={name:"Skeleton",extends:W,inheritAttrs:!1,computed:{containerStyle:function(){return this.size?{width:this.size,height:this.size,borderRadius:this.borderRadius}:{width:this.width,height:this.height,borderRadius:this.borderRadius}},dataP:function(){return J(Y({},this.shape,this.shape))}}},ne=["data-p"];function se(n,s,r,y,h,c){return u(),p("div",T({class:n.cx("root"),style:[n.sx("root"),c.containerStyle],"aria-hidden":"true"},n.ptmi("root"),{"data-p":c.dataP}),null,16,ne)}w.render=se;const ae={key:0},oe={class:"page-header"},re={class:"text-muted"},ie={class:"grid-2"},le={class:"kv"},ue={class:"kv"},de={class:"kv"},pe={class:"kv"},ve={class:"card-header-row"},ce={key:0,class:"skeleton-stack"},me={key:1},ye={class:"kv"},he={class:"kv"},fe={key:0,class:"kv"},ge={style:{color:"var(--p-red-400)"}},_e={key:2,class:"text-muted"},ke={class:"kv-row"},Se={class:"kv"},be={class:"kv"},we={class:"kv"},xe={class:"kv"},Ce={key:0,class:"kv"},Pe={key:1,class:"kv"},Ae={class:"action-btns"},$e={class:"chain-chips"},Ie={key:1,class:"text-muted",style:{"text-align":"center",padding:"60px"}},Re=D({__name:"ServerDetailView",setup(n){const s=H(),r=F(),y=K(),h=E(),c=s.params.id,a=A(null),d=A(null),S=A(!1);V(async()=>{await r.fetchAll(),a.value=r.getServer(c),await x(),await y.fetchAll()});const x=async()=>{S.value=!0;try{d.value=await r.fetchHealth(c)}catch{d.value=null}finally{S.value=!1}},C=()=>y.chains.filter(v=>{var t;return(t=v.nodes)==null?void 0:t.some(P=>P.server_id===c)}),R=async()=>{try{const v=await(await I(async()=>{const{serversApi:t}=await import("./client-Bc7kWmUb.js");return{serversApi:t}},__vite__mapDeps([0,1,2,3]))).serversApi.scan(c);h.add({severity:"info",summary:"Scan",detail:JSON.stringify(v),life:5e3})}catch(v){h.add({severity:"error",summary:"Scan failed",detail:String(v),life:5e3})}},z=async()=>{try{const v=await(await I(async()=>{const{serversApi:t}=await import("./client-Bc7kWmUb.js");return{serversApi:t}},__vite__mapDeps([0,1,2,3]))).serversApi.install(c);h.add({severity:"success",summary:"Installed",detail:v.status,life:3e3})}catch(v){h.add({severity:"error",summary:"Install failed",detail:String(v),life:5e3})}};return(v,t)=>{const P=O("Tag");return a.value?(u(),p("div",ae,[e("div",oe,[e("div",null,[e("h2",null,o(a.value.name),1),e("p",re,o(a.value.host)+":"+o(a.value.port)+" · "+o(a.value.username),1)]),e("div",{class:X(["status-dot",a.value.status==="online"?"online":"offline"])},o(a.value.status.toUpperCase()),3)]),e("div",ie,[i(l(_),null,{content:f(()=>[t[4]||(t[4]=e("h4",null,"Connection",-1)),e("div",le,[t[0]||(t[0]=e("span",null,"Host",-1)),e("strong",null,o(a.value.host),1)]),e("div",ue,[t[1]||(t[1]=e("span",null,"SSH Port",-1)),e("strong",null,o(a.value.port),1)]),e("div",de,[t[2]||(t[2]=e("span",null,"User",-1)),e("strong",null,o(a.value.username),1)]),e("div",pe,[t[3]||(t[3]=e("span",null,"Auth",-1)),e("strong",null,o(a.value.auth_method),1)])]),_:1}),i(l(_),null,{content:f(()=>[e("div",ve,[t[5]||(t[5]=e("h4",null,"Health",-1)),i(l(g),{icon:"pi pi-refresh",text:"",rounded:"",size:"small",loading:S.value,onClick:x},null,8,["loading"])]),S.value?(u(),p("div",ce,[i(l(w),{height:"18px"}),i(l(w),{height:"18px",width:"80%"}),i(l(w),{height:"18px",width:"60%"})])):d.value?(u(),p("div",me,[e("div",ye,[t[6]||(t[6]=e("span",null,"SSH",-1)),e("strong",{style:$({color:d.value.online?"var(--p-green-500)":"var(--p-red-500)"})},o(d.value.online?`Connected (${d.value.latency_ms}ms)`:"Unreachable"),5)]),e("div",he,[t[7]||(t[7]=e("span",null,"Xray",-1)),e("strong",{style:$({color:d.value.xray_running?"var(--p-green-500)":"var(--p-red-500)"})},o(d.value.xray_running?`Running ${d.value.xray_version}`:"Stopped"),5)]),d.value.error?(u(),p("div",fe,[t[8]||(t[8]=e("span",null,"Error",-1)),e("strong",ge,o(d.value.error),1)])):b("",!0)])):(u(),p("p",_e,"No health data"))]),_:1})]),i(l(_),{style:{"margin-top":"16px"}},{content:f(()=>{var m;return[t[15]||(t[15]=e("h4",null,"System",-1)),e("div",ke,[e("div",Se,[t[9]||(t[9]=e("span",null,"OS",-1)),e("strong",null,o(a.value.os||"Unknown"),1)]),e("div",be,[t[10]||(t[10]=e("span",null,"Arch",-1)),e("strong",null,o(a.value.arch||"Unknown"),1)]),e("div",we,[t[11]||(t[11]=e("span",null,"Source",-1)),e("strong",null,o(a.value.source),1)]),e("div",xe,[t[12]||(t[12]=e("span",null,"Created",-1)),e("strong",null,o(new Date(a.value.created_at).toLocaleString()),1)]),a.value.last_seen?(u(),p("div",Ce,[t[13]||(t[13]=e("span",null,"Last Seen",-1)),e("strong",null,o(new Date(a.value.last_seen).toLocaleString()),1)])):b("",!0),(m=a.value.tags)!=null&&m.length?(u(),p("div",Pe,[t[14]||(t[14]=e("span",null,"Tags",-1)),e("strong",null,o(a.value.tags.join(", ")),1)])):b("",!0)])]}),_:1}),i(l(_),{style:{"margin-top":"16px"}},{content:f(()=>[t[16]||(t[16]=e("h4",null,"Actions",-1)),e("div",Ae,[i(l(g),{label:"Scan",icon:"pi pi-radar",outlined:"",onClick:R}),i(l(g),{label:"Install Xray",icon:"pi pi-download",onClick:z}),i(l(g),{label:"Import Config",icon:"pi pi-file-import",outlined:""}),i(l(g),{label:"Test Connection",icon:"pi pi-plug",outlined:"",onClick:x})])]),_:1}),C().length>0?(u(),B(l(_),{key:0,style:{"margin-top":"16px"}},{content:f(()=>[e("h4",null,"Chains on this server ("+o(C().length)+")",1),e("div",$e,[(u(!0),p(N,null,j(C(),m=>(u(),p("span",{key:m.id,class:"chain-chip"},[i(P,{value:m.status,severity:m.status==="active"?"success":"info",rounded:!0},null,8,["value","severity"]),e("span",null,o(m.name),1)]))),128))])]),_:1})):b("",!0)])):(u(),p("div",Ie," Loading... "))}}}),He=U(Re,[["__scopeId","data-v-5e815fea"]]);export{He as default};
