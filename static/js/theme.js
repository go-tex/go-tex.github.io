(function(){
  var r=document.documentElement, k='theme';
  function set(m){ if(m==='system'){r.removeAttribute('data-theme');localStorage.removeItem(k);}
    else{r.setAttribute('data-theme',m);localStorage.setItem(k,m);} lbl(m); }
  function cur(){ return localStorage.getItem(k)||'system'; }
  function lbl(m){ var b=document.getElementById('themebtn'); if(b) b.textContent=
    ({system:'Theme: System',light:'Theme: Light',dark:'Theme: Dark'})[m]; }
  var s=cur(); if(s!=='system') r.setAttribute('data-theme',s);
  document.addEventListener('DOMContentLoaded',function(){ lbl(cur());
    var b=document.getElementById('themebtn'); if(b) b.addEventListener('click',function(){
      var order=['system','light','dark'], m=order[(order.indexOf(cur())+1)%3]; set(m); }); });
})();