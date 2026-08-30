(function(){
  var r=document.documentElement, k='theme';
  function set(m){ if(m==='system'){r.removeAttribute('data-theme');localStorage.removeItem(k);}
    else{r.setAttribute('data-theme',m);localStorage.setItem(k,m);} }
  function cur(){ return localStorage.getItem(k)||'system'; }
  // Apply the stored choice before first paint so the page does not flash.
  var s=cur(); if(s!=='system') r.setAttribute('data-theme',s);
  // The theme toggle now lives inside the wasm app (its toolbar). It calls
  // gotexApplyThemeMode(mode) to put the choice on the page; the host page's
  // MutationObserver on data-theme then recolours the canvas via gotexSetTheme.
  globalThis.gotexApplyThemeMode=function(m){ set(m); };
  // The page reads this to seed the app button's initial selection once wasm is ready.
  globalThis.gotexThemeMode=cur;
})();
